// Package daemon runs compactor's event-driven mode: a long-lived
// process that reacts to MinIO bucket notifications (via NATS
// JetStream) by reconciling the affected day's rollup shortly after,
// and exposes Kubernetes liveness/readiness probes for a Deployment.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/pflege-de-labs/compactor/internal/metrics"
	"github.com/pflege-de-labs/compactor/internal/notify"
	"github.com/pflege-de-labs/compactor/internal/rollup"
)

type Deps struct {
	Engine       *rollup.Engine
	SourcePrefix string
	NATS         notify.NATSConfig
	Debounce     time.Duration
	HTTPAddr     string
	Logger       *slog.Logger
	Metrics      *metrics.Recorder
}

// Listen blocks until ctx is cancelled (e.g. on SIGTERM), consuming
// bucket notifications and reconciling the affected day's rollup after
// each debounce window.
func Listen(ctx context.Context, deps Deps) error {
	health := &Health{}
	httpServer := &http.Server{Addr: deps.HTTPAddr, Handler: health.Handler()}

	httpErrCh := make(chan error, 1)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			httpErrCh <- err
			return
		}
		httpErrCh <- nil
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	subscriber, err := notify.ConnectNATS(ctx, deps.NATS)
	if err != nil {
		return fmt.Errorf("daemon: %w", err)
	}
	defer subscriber.Close()

	debouncer := NewDebouncer(deps.Debounce, func(day time.Time) {
		deps.reconcile(ctx, day)
	})
	defer debouncer.Stop()

	health.SetReady(true)
	deps.Logger.Info("listening for bucket notifications", "http_addr", deps.HTTPAddr, "nats_url", deps.NATS.URL, "subject", deps.NATS.Subject)

	subscribeErr := subscriber.Subscribe(ctx, func(ev notify.Event) error {
		day, ok := dayFromKey(ev.Key, deps.SourcePrefix)
		if !ok {
			return nil
		}
		debouncer.Notify(day)
		return nil
	})
	health.SetReady(false)

	if subscribeErr != nil {
		return fmt.Errorf("daemon: %w", subscribeErr)
	}
	select {
	case err := <-httpErrCh:
		return err
	default:
		return nil
	}
}

func (d Deps) reconcile(ctx context.Context, day time.Time) {
	start := time.Now()
	result, err := d.Engine.RunHourlyDayRollup(ctx, day)
	if d.Metrics != nil {
		d.Metrics.RecordDayRollup(ctx, time.Since(start), result.ItemsAppended, result.SkippedBad, result.NoOp, err)
	}

	log := d.Logger.With("day", day.Format("2006-01-02"))
	if err != nil {
		log.Error("day rollup failed", "error", err)
		return
	}
	log.Info("day rollup reconciled",
		"appended", result.ItemsAppended,
		"skipped_bad", result.SkippedBad,
		"noop", result.NoOp,
	)
}
