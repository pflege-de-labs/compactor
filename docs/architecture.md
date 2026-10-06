# compactor architecture

compactor rolls up per-event JSON objects in an S3-compatible bucket,
organized as `year/month/day/` prefixes, into day → week → month
JSONL rollups — so DuckDB can query them without touching thousands of
small objects — while preserving whatever client-side encryption the
source data had. It runs in two modes, sharing the same reconciliation
core:

- **CronJob / one-shot CLI**: `rollup hourly` / `rollup promote-week` /
  `rollup promote-month` / `materialize` / `checkpoint ...`, triggered
  externally on a schedule.
- **Deployment / event-driven**: `listen`, a long-lived process that
  reconciles a day's rollup shortly after a MinIO bucket notification
  arrives, and serves Kubernetes liveness/readiness probes.

See `docs/adr/` for the reasoning behind the decisions summarized here.

## Package layout

```
cmd/compactor/       CLI entrypoint (kong), //go:generate directives for man/completions
internal/
  cli/                kong command wiring, App bootstrap (config -> deps), man/completions subcommands
  config/             Config schema (kong flags + kong-yaml), XDG default path
  storage/            ObjectStore (S3/MinIO), Lister (discovery), in-memory fake
  crypto/              Encryptor/Decryptor/Codec/Registry, age backend, no-op backend
  checkpoint/          Day/Week/Month checkpoint types + S3-backed Store
  rollup/               Engine: hourly day rollup, week/month promotion
  materialize/           decrypt-on-read scratch file for DuckDB
  notify/                 MinIO/S3 bucket notification parsing + NATS JetStream consumer
  daemon/                  `listen` mode: debounced event -> reconcile trigger, health probes
  metrics/                  OTEL meter provider + instruments, exported via autoexport
```

## Data flow

1. `compactor rollup hourly` discovers new source objects under a
   day's prefix (`storage.PollLister`), filters out ones already in
   that day's checkpoint, decrypts/validates/appends each as one JSONL
   line to the day's rollup, re-encrypts the whole rollup if any
   content is encrypted, and does a conditional overwrite. See
   [ADR 0001](adr/0001-poll-based-discovery-with-pluggable-lister.md),
   [ADR 0003](adr/0003-rollup-strategy-incremental-day-vs-rebuild-from-children.md).
2. `compactor rollup promote-week` / `promote-month` rebuild the
   parent rollup from its already-consolidated children (days for a
   week, weeks for a month), short-circuiting when nothing changed.
   See [ADR 0003](adr/0003-rollup-strategy-incremental-day-vs-rebuild-from-children.md).
3. `compactor materialize <key>` decrypts a rollup into a local
   scratch file for an external DuckDB query to read. See
   [ADR 0005](adr/0005-decrypt-on-read-materialize-path-for-duckdb.md).
4. `compactor checkpoint show|reset` inspects or (as an explicit,
   confirmed operator escape hatch) clears a checkpoint's tracking
   state.
5. `compactor listen` consumes MinIO bucket notifications from NATS
   JetStream (`internal/notify`), debounces bursts per day
   (`internal/daemon`), and calls the *same*
   `Engine.RunHourlyDayRollup` as step 1 — notifications are a trigger,
   not a second source of truth. Serves `/healthz`/`/readyz` for a
   Kubernetes Deployment. See
   [ADR 0006](adr/0006-event-driven-listen-mode-via-nats-jetstream.md).

Every rollup and checkpoint write is a conditional PUT
(`IfMatchETag`/`IfNoneMatch`), and the rollup object is always written
before its checkpoint, so crashes and overlapping runs are safe to
retry. See [ADR 0004](adr/0004-checkpoint-and-conditional-put-concurrency-model.md).

## Encryption

Source objects may or may not be encrypted, and different source
objects within the same rollup unit may use *different* schemes (or
mix plaintext and encrypted) — each is detected independently (key
extension, falling back to a content-header sniff) via
`crypto.Registry` and decoded with its own codec. Rollup *output*,
however, always uses a single configured scheme
(`rollup.Engine.OutputScheme`, from `encryption.scheme`): a rollup is
encrypted with it if any contributing source event was encrypted (in
any scheme); a rollup already encrypted never downgrades to plaintext.
See [ADR 0002](adr/0002-encryptor-decryptor-abstraction-and-detection.md)
and [ADR 0008](adr/0008-decouple-source-decode-scheme-from-rollup-output-scheme.md)
(which decoupled source decode scheme from output scheme).

Each source object may hold one JSON event or a batch of many
(JSONL) — `rollup.AppendJSONLines` handles both uniformly. See
ADR 0008.

Codecs ship for: `none` (no-op), `age` (`filippo.io/age`, both read
and write — MVP's/compactor's own output scheme), and
`aes-ctr-gzip` (`crypto.AESCTRGzipCodec`, **source-decode-only** — AES-CTR
+ gzip with the IV embedded in the object's filename, matching a
Redpanda Connect/Benthos pipeline this project reads from; its key
comes from the `COMPACTOR_SOURCE_AES_CTR_GZIP_KEY` env var, never
YAML config). Adding another source-decode-only or read/write backend
means implementing `crypto.Codec` and registering it in
`internal/cli/app.go`'s `buildRegistry` — no changes needed in
`rollup.Engine`.

## Configuration

`internal/config` defines each section (`S3`, `Paths`, `Encryption`,
`Rollup`, `Materialize`, `Listen`, `Logging`) as a plain struct of kong
flags (`help`/`name`/`default`/`enum` tags). `internal/cli.CLI` embeds
every section (`embed:"" prefix:"s3-"` etc.) so each field is both a
CLI flag (e.g. `--s3-bucket`, `--encryption-age-recipients`) and,
through `github.com/alecthomas/kong-yaml`'s `Loader` wired via
`kong.Configuration(kongyaml.Loader, defaultPath)` in
`cmd/compactor/main.go`, a YAML key — `kongyaml.Loader` resolves a
value by joining a flag's full dash-separated name and walking the
YAML file's nesting to match it, which is why the YAML schema uses
dashed keys (`source-prefix`, not `source_prefix`). A `--config` flag
(`kong.ConfigFlag`) lets an explicit file override the XDG default
(`$XDG_CONFIG_HOME/compactor/config.yaml`). Precedence: explicit flag
> `--config` file (or the XDG default) > the field's `default:` tag.
`internal/cli.Bootstrap` runs after kong has already parsed and
resolved everything; it just copies the populated CLI fields into a
`config.Config` and builds every other dependency
(`storage.ObjectStore`, `crypto.Registry`, `checkpoint.Store`,
`rollup.Engine`) from it once, per invocation.

## Metrics

Both modes report OpenTelemetry metrics (`internal/metrics`) via
`go.opentelemetry.io/contrib/exporters/autoexport`, which reads the
standard `OTEL_METRICS_EXPORTER`/`OTEL_EXPORTER_OTLP_*` environment
variables — compactor's own config carries no OTEL fields. The
CronJob path flushes explicitly (`App.Shutdown`) before exiting; the
Deployment path's periodic reader exports in the background for the
life of the process. See
[ADR 0007](adr/0007-otel-metrics-via-autoexport.md).

## Generated files

`compactor man` and `compactor completions bash|zsh|fish` are real
subcommands (`internal/cli/gen.go`) using `mango-kong` (man page) and
`miekg/king` (completions) against the live `*kong.Context` — not a
separate generator binary, and no assumption that a `docs/` directory
or source checkout exists at runtime (they write to stdout by default,
or `--output <path>`). `docs/man/compactor.1` and `docs/completions/*`
are checked-in snapshots regenerated via `go generate ./...` from
`cmd/compactor` (the `//go:generate` directives there just run
`go run . man -o ...` / `go run . completions <shell> -o ...`).
Regenerate after changing the CLI surface.

`*cli.App` (S3/encryption/checkpoint/rollup/metrics) is bootstrapped
lazily via `kong.BindSingletonProvider` in `cmd/compactor/main.go`,
not unconditionally in `main()` — so `man`/`completions`, which need
none of it, work with zero configuration (no bucket, no credentials).
It's still constructed exactly once and shared if a real command's
`Run()` does ask for it.

## CI/CD

GitHub Actions builds and cosign-signs a container image per commit
(gated on a shared `go-checks.yml`), and a `v*` tag rebuilds, re-gates
and signs the release image and publishes signed binaries with SBOMs.
Renovate keeps dependencies and Action pins current. See
[ADR 0009](adr/0009-ci-cd-pipeline.md).

## Deferred (see plan / ADRs for detail)

- PGP / static-symmetric-key `crypto.Codec` backends.
- Kafka/AMQP/Redis notification backends (`internal/notify` supports
  only NATS JetStream today; `notify.Event`/`ParseEvents` are
  transport-agnostic, so another backend is a new file, not a rewrite).
- A `compactor query` convenience wrapper around materialize + exec'ing
  DuckDB directly.
- A stronger lock/lease mechanism beyond optimistic conditional PUT.
- Dead-letter handling for malformed source events (currently: log,
  skip, mark processed so it isn't retried forever).
- A coverage gate (CI reports total coverage but doesn't enforce a
  threshold yet, see ADR 0009) and the Helm chart publishing workflow.
