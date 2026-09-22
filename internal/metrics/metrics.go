// Package metrics instruments compactor with OpenTelemetry metrics,
// exported via OTLP to whatever collector (e.g. Grafana Alloy) is
// configured through the standard OTEL_EXPORTER_OTLP_* /
// OTEL_METRICS_EXPORTER environment variables. The same mechanism
// covers both the short-lived CronJob path (an explicit Shutdown
// flushes before the process exits) and the long-lived Deployment path
// (a periodic reader exports in the background).
package metrics

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// Recorder wraps the OTEL instruments compactor reports. All fields are
// safe to use concurrently.
type Recorder struct {
	provider *sdkmetric.MeterProvider

	dayRollupRuns  metric.Int64Counter
	itemsAppended  metric.Int64Counter
	itemsSkipped   metric.Int64Counter
	promotionRuns  metric.Int64Counter
	rollupErrors   metric.Int64Counter
	rollupDuration metric.Float64Histogram
}

// NewRecorder builds a MeterProvider from the environment (via
// autoexport: OTEL_METRICS_EXPORTER=otlp by default, or "prometheus" /
// "console" / "none") and registers compactor's instruments.
func NewRecorder(ctx context.Context, serviceName string) (*Recorder, error) {
	reader, err := autoexport.NewMetricReader(ctx)
	if err != nil {
		return nil, fmt.Errorf("metrics: build reader: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithAttributes(semconv.ServiceName(serviceName)),
	)
	if err != nil {
		return nil, fmt.Errorf("metrics: build resource: %w", err)
	}

	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(reader),
		sdkmetric.WithResource(res),
	)
	meter := provider.Meter("github.com/pflege-de-labs/compactor")

	r := &Recorder{provider: provider}

	if r.dayRollupRuns, err = meter.Int64Counter("compactor.day_rollup.runs",
		metric.WithDescription("Day rollup reconciliation runs, by status and no-op.")); err != nil {
		return nil, fmt.Errorf("metrics: %w", err)
	}
	if r.itemsAppended, err = meter.Int64Counter("compactor.day_rollup.items_appended",
		metric.WithDescription("Source events successfully folded into a day rollup.")); err != nil {
		return nil, fmt.Errorf("metrics: %w", err)
	}
	if r.itemsSkipped, err = meter.Int64Counter("compactor.day_rollup.items_skipped",
		metric.WithDescription("Source events skipped as malformed or undecryptable.")); err != nil {
		return nil, fmt.Errorf("metrics: %w", err)
	}
	if r.promotionRuns, err = meter.Int64Counter("compactor.promotion.runs",
		metric.WithDescription("Week/month rebuild-from-children runs, by level, status and no-op.")); err != nil {
		return nil, fmt.Errorf("metrics: %w", err)
	}
	if r.rollupErrors, err = meter.Int64Counter("compactor.rollup.errors",
		metric.WithDescription("Rollup operations that returned an error, by op.")); err != nil {
		return nil, fmt.Errorf("metrics: %w", err)
	}
	if r.rollupDuration, err = meter.Float64Histogram("compactor.rollup.duration",
		metric.WithDescription("Rollup operation wall-clock duration."),
		metric.WithUnit("s")); err != nil {
		return nil, fmt.Errorf("metrics: %w", err)
	}

	return r, nil
}

// Shutdown flushes any buffered metrics and releases exporter
// resources. Callers should treat a Shutdown error as non-fatal:
// metrics delivery failing must never fail an otherwise-successful
// rollup run.
func (r *Recorder) Shutdown(ctx context.Context) error {
	return r.provider.Shutdown(ctx)
}

// RecordDayRollup reports one RunHourlyDayRollup invocation.
func (r *Recorder) RecordDayRollup(ctx context.Context, dur time.Duration, appended, skippedBad int, noOp bool, err error) {
	status := statusOf(err)
	attrs := metric.WithAttributes(attribute.String("status", status), attribute.Bool("noop", noOp))

	r.dayRollupRuns.Add(ctx, 1, attrs)
	r.itemsAppended.Add(ctx, int64(appended))
	r.itemsSkipped.Add(ctx, int64(skippedBad))
	r.rollupDuration.Record(ctx, dur.Seconds(), metric.WithAttributes(attribute.String("op", "day")))
	if err != nil {
		r.rollupErrors.Add(ctx, 1, metric.WithAttributes(attribute.String("op", "day")))
	}
}

// RecordPromotion reports one RunPromoteWeek/RunPromoteMonth invocation.
func (r *Recorder) RecordPromotion(ctx context.Context, level string, dur time.Duration, noOp bool, err error) {
	status := statusOf(err)
	r.promotionRuns.Add(ctx, 1, metric.WithAttributes(
		attribute.String("level", level), attribute.String("status", status), attribute.Bool("noop", noOp)))
	r.rollupDuration.Record(ctx, dur.Seconds(), metric.WithAttributes(attribute.String("op", level)))
	if err != nil {
		r.rollupErrors.Add(ctx, 1, metric.WithAttributes(attribute.String("op", level)))
	}
}

func statusOf(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}
