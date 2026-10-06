# 10. Helm chart with independent listener and CronJob workloads

## Status

Accepted

## Context

compactor has two operating modes (ADR 0006): an event-driven `listen`
daemon and scheduled `rollup ...` runs. The listener only reconciles
*day* rollups; week and month promotion has no event trigger, so a
realistic deployment needs both a long-running workload and CronJobs.
A chart that forced one mode per release would make that two installs.

## Decision

`charts/compactor` renders any combination of: the listener (a
Deployment, or a StatefulSet via `listen.kind`, with a headless Service),
and three CronJobs (`rollup hourly`, `promote-week`, `promote-month`),
each behind its own `enabled` toggle. Defaults run the listener plus the
two promotion CronJobs; `hourly` is off because it is redundant while the
listener runs, but is the primary mode when `listen.enabled` is false.

- **Config** is the `config:` values tree rendered into a ConfigMap with
  the dashed keys kong-yaml expects (ADR on configuration in
  `docs/architecture.md`), mounted as `--config`. `listen.http-addr` is
  derived from `listen.port` so probes and config cannot disagree. An
  `existingConfig` ConfigMap replaces it entirely.
- **Secrets stay out of config**: S3 credentials, the age identity file
  and the AES source key are each an `existingSecret` reference (env,
  mounted file, `secretKeyRef`). An inline credentials value exists for
  convenience, and ambient identity (IRSA/workload identity through
  `serviceAccount.annotations`) is the intended production path.
- **Hardened by default**: non-root uid 65532, read-only root filesystem,
  dropped capabilities, `RuntimeDefault` seccomp, no ServiceAccount token
  (compactor never calls the Kubernetes API), `/tmp` as an `emptyDir`
  doubling as `HOME` for XDG path resolution. The image runs as root
  unless the chart says otherwise, so the security context is not optional
  decoration.
- **No Service/ServiceMonitor for metrics**: metrics are pushed over OTLP
  (ADR 0007), so there is nothing to scrape. The only Service is the
  StatefulSet's headless one.
- Optional `PodDisruptionBudget` (listener) and `NetworkPolicy` (all
  pods; deny ingress, allow DNS plus user-supplied egress, because S3, NATS
  and the collector are not knowable in advance).
- **Replicas** of the listener share one durable JetStream consumer, so a
  StatefulSet adds stable pod names but no per-pod consumer state. Concurrent
  reconciles of the same day are safe because every rollup and checkpoint
  write is a conditional PUT (ADR 0004).

The chart is versioned independently of the application and published as
an OCI artifact to `ghcr.io/<owner>/charts` by
`.github/workflows/release_helm_chart.yml` when `version` in `Chart.yaml`
is raised on `main`. CI lints and renders every `ci/*-values.yaml` shape
and asserts what each renders (see ADR 0009 for the pipeline).

## Consequences

- `promote-week` and `promote-month` target the current ISO week/month.
  Events landing after the last promotion before a boundary are not folded
  into the closing period without a manual `--week`/`--month` run. A CLI
  option to promote a trailing window would close this gap and is a
  natural follow-up.
- Each CronJob mounts the same ConfigMap, so a config change rolls the
  listener (checksum annotation) and takes effect on the next scheduled
  run for the CronJobs.
- Default `image.tag` is the chart `appVersion`; it must be kept pointing
  at a published release when the chart version is bumped.
