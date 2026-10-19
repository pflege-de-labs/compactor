# Running compactor

`compactor` is one binary with two modes:

- **One-shot commands** (`rollup …`, `checkpoint …`, `materialize`) run once and
  exit. In Kubernetes they run as CronJobs.
- **`listen`** is a long-running daemon. It runs a day rollup whenever MinIO
  publishes a notification over NATS.

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
2. Delete the day rollup object under `<rollup-prefix>/day/YYYY/MM/DD.jsonl[.age]`.
   `checkpoint reset` leaves it in place, and the next create-only write would
   then fail with `precondition failed`.
3. Run `compactor checkpoint reset --day YYYY-MM-DD --confirm`.
4. Run `compactor rollup hourly --day YYYY-MM-DD`.
5. Run `rollup promote-week` and `rollup promote-month` for the affected periods.

## Environment variables

| Variable | Used for |
|---|---|
| `COMPACTOR_SOURCE_AES_CTR_GZIP_KEY` | Hex-encoded AES key (16, 24 or 32 bytes) for `.jsonl.gz.enc` source objects. Without it, a run that finds such an object fails instead of decoding it. Chart: `sourceAesCtrGzipKey.existingSecret`. |
| `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`, `AWS_PROFILE`, … | S3 credentials, resolved through the standard AWS SDK chain. Chart: `credentials.existingSecret`, or IRSA or workload identity through `serviceAccount.annotations`. |
| `OTEL_METRICS_EXPORTER`, `OTEL_EXPORTER_OTLP_*`, `OTEL_RESOURCE_ATTRIBUTES`, … | OpenTelemetry metrics export, see [ADR 0007](adr/0007-otel-metrics-via-autoexport.md). Chart: `extraEnv`. |
| `XDG_CONFIG_HOME` | Default `--config` location: `$XDG_CONFIG_HOME/compactor/config.yaml`. |
| `XDG_CACHE_HOME` | Default `materialize --out`: `$XDG_CACHE_HOME/compactor/materialized`. |

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
