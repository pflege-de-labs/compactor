# 7. OTEL metrics via autoexport, one mechanism for both CronJob and Deployment

## Status

Accepted

## Context

compactor runs in two very different lifecycles: a short-lived
CronJob-triggered CLI invocation (`rollup hourly`/`promote-week`/
`promote-month`) and a long-lived Deployment (`listen`). Metrics need
to reach an OTEL collector (e.g. Grafana Alloy) or Prometheus in both
cases. The short-lived case rules out relying purely on a periodic
background exporter — the process may exit before the next export
tick — which usually pushes people toward a second mechanism
(Prometheus Pushgateway) just for batch jobs.

## Decision

Use the OTEL Go SDK's metrics with a `go.opentelemetry.io/contrib/
exporters/autoexport`-built reader in both modes — no separate
Pushgateway code path. `autoexport.NewMetricReader` reads the standard
`OTEL_METRICS_EXPORTER` environment variable (`otlp` by default, or
`prometheus` / `console` / `none`) and the matching `OTEL_EXPORTER_
OTLP_*` variables for endpoint/protocol, so compactor's own config
schema carries no OTEL-specific fields at all — it's operator/platform
configuration, not application configuration.

For the CronJob path, `main.go` calls `App.Shutdown(ctx)` (which
flushes the `MeterProvider`) after the command finishes and before the
process exits, so metrics from a run that completes in seconds still
land. For the Deployment path, the same `MeterProvider`'s periodic
reader exports in the background for as long as the process runs;
`Shutdown` on SIGTERM-triggered exit flushes whatever's pending.

Instrumentation lives at the CLI/daemon call sites
(`internal/cli/rollup_*.go`, `internal/daemon/daemon.go`), wrapping
calls to `rollup.Engine`, rather than inside `rollup.Engine` itself —
keeps the engine free of an OTEL dependency and metrics as a
cross-cutting concern at the boundary.

## Consequences

- Zero app-level config for where metrics go; changing collector
  endpoint or switching to Prometheus-pull is a Kubernetes
  Job/Deployment env var change, not a compactor config change or
  code change.
- A metrics backend being unreachable is explicitly non-fatal:
  `App.Shutdown`'s error is logged, never propagated as the command's
  own failure — observability must not be able to fail an otherwise-
  successful rollup.
- If an operator specifically wants classic Prometheus Pushgateway
  (rather than an OTLP-receiving collector or Prometheus scrape),
  that's out of scope here — route it through an OTEL Collector's
  Pushgateway-capable exporter, since the OTEL metrics SDK itself has
  no Pushgateway integration.
