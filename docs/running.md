# Running compactor

`compactor` is one binary with two modes:

- **One-shot commands** (`rollup …`, `checkpoint …`, `materialize`) run once and
  exit. In Kubernetes they run as CronJobs.
- **`listen`** is a long-running daemon. It runs a day rollup whenever MinIO
  (or SILO, Pigsty's drop-in MinIO fork) publishes a notification over NATS.

Configuration comes from flags, from a YAML file passed with `--config`, or from
the defaults. Flags win over the file, and the file wins over the defaults.
Every flag is listed in `compactor --help` and in
[`config.example.yaml`](../config.example.yaml). Flags cannot be set through
environment variables. The environment variables compactor does read are
listed [below](#environment-variables).

## Commands

| Command | Does | Typical schedule |
|---|---|---|
| `rollup hourly [--day YYYY-MM-DD]` | Appends new source events to the day rollup. Without `--day`: today plus `--rollup-straggler-days` prior days. | hourly |
| `rollup promote-week [--week YYYY-Www]` | Rebuilds a week rollup from its day rollups. Default: current ISO week. | daily |
| `rollup promote-month [--month YYYY-MM]` | Rebuilds a month rollup from its week rollups. Default: current month. | daily |
| `rollup backfill --from YYYY-MM-DD --to YYYY-MM-DD [--no-promote]` | Runs the day rollup for every day in the range, then promotes every week and month it touches (`--no-promote` skips that). Idempotent; rerun to pick up deferred days. | manual |
| `listen` | Event-driven day rollups, serves `/healthz` and `/readyz` on `--listen-http-addr`. | Deployment |
| `checkpoint show --day/--week/--month …` | Prints a checkpoint as JSON. | manual |
| `checkpoint reset --day/--week/--month … --confirm` | Clears a checkpoint's tracking state. It does not delete the rollup. | manual |
| `materialize <key> [--out DIR] [--keep]` | Decrypts a rollup into a local scratch file for DuckDB. | manual |
| `man`, `completions bash\|zsh\|fish` | Generates the man page or a completion script. | build time |

Examples:

```sh
compactor --config config.yaml rollup hourly
compactor --config config.yaml rollup hourly --day 2026-10-08
compactor --config config.yaml checkpoint show --day 2026-10-08
```

In Kubernetes, start a one-off run from the chart's CronJob:

```sh
kubectl get cronjobs   # names are <fullname>-hourly, -promote-week, -promote-month
kubectl create job --from=cronjob/<fullname>-hourly rollup-manual-1
kubectl logs -f job/rollup-manual-1
```

### Rebuilding a day

Use this when a day rollup holds bad data, for example events that were decoded
without the right key:

1. Suspend the CronJobs (`cronJobs.<job>.suspend: true`) and stop the listener.
2. Run `compactor checkpoint reset --day YYYY-MM-DD --confirm`, so every source
   object of the day counts as new again.
3. Run `compactor rollup backfill --from YYYY-MM-DD --to YYYY-MM-DD` for the day.
   It overwrites the old day rollup and rebuilds the affected week and month.

A day whose rollup no longer matches its checkpoint (a crash between the two
writes, or a deleted or edited rollup) is rebuilt from all its source objects
on the next run that sees a new source object, see
[ADR 0013](adr/0013-rebuild-day-when-rollup-and-checkpoint-disagree.md).

### Backfilling history

`rollup backfill` processes historical data in one run. The image has no shell,
so run it as a one-off Job cloned from the release's hourly CronJob, which
carries the config, credentials and resources:

```sh
kubectl -n compactor create job compactor-events-backfill \
  --from=cronjob/compactor-events-hourly --dry-run=client -o yaml \
| yq '.spec.template.spec.containers[0].args = ["--config=/etc/compactor/config.yaml", "rollup", "backfill", "--from=2026-01-01", "--to=2026-10-17"]' \
| kubectl apply -f -
kubectl -n compactor logs -f job/compactor-events-backfill
```

Memory: a day, week or month rollup is held in memory while it is built, at
about 1.5x its uncompressed JSONL size. Size the Job's memory limit for the
largest day; `runtime.memory-limit-ratio` lets the GC use that limit. Week and
month promotion hold the whole week or month, so for high-volume history pass
`--no-promote` and promote only periods that fit.

Keep `--to` before the hourly job's window (today minus `rollup.straggler-days`)
so both never write the same day; with `s3.if-match: false` an overlap can lose
an update. A failed or deferred day is safe to retry: rerun the Job with the same
range, already processed source objects are skipped.

## Environment variables

| Variable | Used for |
|---|---|
| `COMPACTOR_SOURCE_AES_CTR_GZIP_KEY` | Hex-encoded AES key (16, 24 or 32 bytes) for `.jsonl.gz.enc` source objects. Without it, a run that finds such an object fails instead of decoding it. Chart: `sourceAesCtrGzipKey.existingSecret`. |
| `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`, `AWS_PROFILE`, … | S3 credentials, resolved through the standard AWS SDK chain. Chart: `credentials.existingSecret`, or IRSA or workload identity through `serviceAccount.annotations`. |
| `OTEL_METRICS_EXPORTER`, `OTEL_EXPORTER_OTLP_*`, `OTEL_RESOURCE_ATTRIBUTES`, … | OpenTelemetry metrics export, see [ADR 0007](adr/0007-otel-metrics-via-autoexport.md). Chart: `extraEnv`. |
| `XDG_CONFIG_HOME` | Default `--config` location: `$XDG_CONFIG_HOME/compactor/config.yaml`. |
| `XDG_CACHE_HOME` | Default `materialize --out`: `$XDG_CACHE_HOME/compactor/materialized`. |
| `GOMEMLIMIT` | Fixed Go memory limit (e.g. `900MiB`); overrides `runtime.memory-limit-ratio`. |
| `AUTOMEMLIMIT` | Overrides `runtime.memory-limit-ratio` (`0.0`–`1.0`), or `off` to disable it. |

Some AWS SDK variables have no effect because compactor sets the value itself:

- `AWS_REGION` and `AWS_DEFAULT_REGION`: overridden by `--s3-region`, which
  defaults to `us-east-1`.
- `AWS_REQUEST_CHECKSUM_CALCULATION` and `AWS_RESPONSE_CHECKSUM_VALIDATION`:
  always `when_required`, for S3-compatible backends.

Backends that reject `If-Match` on PUT (Ceph RGW, e.g. SysEleven) fail every
update with `storage: precondition failed`. Set `s3.if-match: false`
(`--no-s3-if-match`) for them; updates then use a non-atomic HEAD check, so
run a single writer per day, see
[ADR 0012](adr/0012-head-check-fallback-for-backends-without-if-match.md).
