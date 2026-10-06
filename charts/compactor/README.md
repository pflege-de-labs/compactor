# compactor Helm chart

Deploys [compactor](https://github.com/pflege-de-labs/compactor), which rolls up
per-event JSON/JSONL objects in an S3-compatible bucket into day/week/month JSONL.
The chart can run it as an event-driven listener, as CronJobs, or both.

```sh
helm install compactor oci://ghcr.io/pflege-de-labs/charts/compactor \
  --set config.s3.bucket=my-events \
  --set credentials.existingSecret=compactor-s3
```

## Workloads

All three are independent toggles; the defaults run the listener plus the two promotion CronJobs.

| Workload | Value | Runs | Notes |
| --- | --- | --- | --- |
| Listener | `listen.enabled` (default `true`) | `compactor listen` | `listen.kind` is `deployment` or `statefulset`. Consumes MinIO bucket notifications from NATS JetStream, reconciles the affected day, serves `/healthz` and `/readyz` on `listen.port`. Replicas share one durable consumer. |
| Hourly rollup | `cronJobs.hourly.enabled` (default `false`) | `compactor rollup hourly` | Incremental day rollup. Use it instead of the listener, or alongside it as a safety net for missed notifications. |
| Week promotion | `cronJobs.promoteWeek.enabled` (default `true`) | `compactor rollup promote-week` | Rebuilds the **current** ISO week from its day rollups. A no-op when nothing changed. |
| Month promotion | `cronJobs.promoteMonth.enabled` (default `true`) | `compactor rollup promote-month` | Rebuilds the **current** month from its weeks. |

Week and month promotion have no event trigger, which is why the CronJobs exist even
when the listener runs. They always target the current week/month, so events landing
after the last run before a boundary are not folded into the closing period; pass
`cronJobs.promoteWeek.args: ["--week=2026-W40"]` or run the command once by hand to
catch up.

## Configuration

`config` is rendered verbatim into `config.yaml` (mounted at `/etc/compactor` and passed
as `--config`) using the dashed keys from
[`config.example.yaml`](https://github.com/pflege-de-labs/compactor/blob/main/config.example.yaml);
only `config.s3.bucket` is required. `listen.http-addr` is derived from `listen.port` and
`encryption.age.identity-file` from `ageIdentity`. Set `existingConfig` to use a ConfigMap
you manage (key `config.yaml`) instead.

## Secrets

Key material never goes in `config`.

| Value | Purpose |
| --- | --- |
| `credentials.existingSecret` | Secret with `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY`. Leave empty to use IRSA / workload identity via `serviceAccount.annotations`. `credentials.accessKeyId`/`secretAccessKey` render a Secret for you (not for production). |
| `ageIdentity.existingSecret` | Secret holding the age identity file (`key`, default `identity.txt`), mounted read-only; needed to read age data. |
| `sourceAesCtrGzipKey.existingSecret` | Hex AES key enabling the source-decode-only `aes-ctr-gzip` codec, injected as `COMPACTOR_SOURCE_AES_CTR_GZIP_KEY`. |

## Metrics

compactor pushes OpenTelemetry metrics over OTLP; there is nothing to scrape, so the chart
ships no Service or ServiceMonitor. Point it at a collector with `otel.endpoint` /
`otel.protocol`, or set `otel.exporter: none`. Anything else goes through `extraEnv`.

## Security defaults

Pods run as uid 65532 with a read-only root filesystem, no privilege escalation, all
capabilities dropped and the `RuntimeDefault` seccomp profile; `/tmp` is an `emptyDir`
(also the `HOME` for XDG paths and the scratch space for `compactor materialize`). The
ServiceAccount token is not mounted. `networkPolicy.enabled` denies all ingress and limits
egress to DNS plus `networkPolicy.egress`, so you must allow S3, NATS and the OTLP collector.

## Testing

`ci/*-values.yaml` are the deployment shapes CI lints and renders (see the `chart` job in
`.github/workflows/ci.yml`, which also asserts what each one produces).
