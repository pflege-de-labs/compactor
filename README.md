# compactor

Rolls up per-event JSON/JSONL objects in an S3-compatible bucket
(organized as `year/month/day/` prefixes) into day → week → month
JSONL files, preserving whatever client-side encryption the source
data had — so tools like DuckDB can query the result without touching
thousands of small objects.

Runs in two modes that share one reconciliation core:

- **Scheduled / CronJob**: `compactor rollup hourly` (incremental,
  current day + configured stragglers), `compactor rollup
  promote-week` / `promote-month` (rebuild from already-consolidated
  children), `compactor materialize` (decrypt a rollup for DuckDB),
  `compactor checkpoint show|reset`.
- **Event-driven / Deployment**: `compactor listen` reacts to MinIO
  bucket notifications via NATS JetStream, debounces bursts per day,
  and reconciles promptly. Serves `/healthz` + `/readyz` for
  Kubernetes probes.

See [`docs/architecture.md`](docs/architecture.md) for how it fits
together, and [`docs/adr/`](docs/adr/) for the reasoning behind
specific design decisions.

## Quick start

```sh
go build ./...
cp config.example.yaml config.yaml   # edit: bucket, prefixes, encryption
./compactor rollup hourly --config config.yaml
```

Every setting is available both as a `--flag` (run `compactor --help`
for the full list) and as a YAML key in `--config` (or
`$XDG_CONFIG_HOME/compactor/config.yaml` by default) — see
[`config.example.yaml`](config.example.yaml).

Generate the man page or shell completions directly from the binary:

```sh
compactor man > compactor.1
compactor completions bash > compactor.bash
```

## Encryption

Source objects may be plaintext, age-encrypted, or (for one specific
upstream producer) AES-CTR+gzip encrypted — detected per object, and
source objects may mix schemes freely within one rollup. Rollup
*output* always uses one configured scheme (`none` or `age`): a
rollup is encrypted the moment any contributing source event needed
it, and never silently downgrades to plaintext. See
[ADR 0002](docs/adr/0002-encryptor-decryptor-abstraction-and-detection.md)
and
[ADR 0008](docs/adr/0008-decouple-source-decode-scheme-from-rollup-output-scheme.md).

## Metrics

Both modes report OpenTelemetry metrics via OTLP, configured entirely
through the standard `OTEL_*` environment variables (no app config) —
see
[ADR 0007](docs/adr/0007-otel-metrics-via-autoexport.md).

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md) for development setup,
testing, and conventions. For AI coding agents working in this repo,
see [`AGENTS.md`](AGENTS.md).

## Security

See [`SECURITY.md`](SECURITY.md) for how to report a vulnerability.

## License

[MIT](LICENSE.md)
