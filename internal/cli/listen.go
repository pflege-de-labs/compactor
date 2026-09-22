package cli

import (
	"context"

	"github.com/pflege-de-labs/compactor/internal/daemon"
	"github.com/pflege-de-labs/compactor/internal/notify"
)

// ListenCmd runs compactor as a long-lived event-driven daemon,
// intended for a Kubernetes Deployment: it consumes MinIO bucket
// notifications from NATS JetStream, reconciles the affected day's
// rollup shortly after each debounce window, and serves
// /healthz+/readyz for liveness/readiness probes. Runs until ctx is
// cancelled (SIGTERM/SIGINT).
type ListenCmd struct{}

func (c *ListenCmd) Run(ctx context.Context, app *App) error {
	return daemon.Listen(ctx, daemon.Deps{
		Engine:       app.Engine,
		SourcePrefix: app.Config.Paths.SourcePrefix,
		NATS: notify.NATSConfig{
			URL:      app.Config.Listen.NATS.URL,
			Stream:   app.Config.Listen.NATS.Stream,
			Subject:  app.Config.Listen.NATS.Subject,
			Consumer: app.Config.Listen.NATS.Consumer,
		},
		Debounce: app.Config.Listen.Debounce,
		HTTPAddr: app.Config.Listen.HTTPAddr,
		Logger:   app.Logger,
		Metrics:  app.Metrics,
	})
}
