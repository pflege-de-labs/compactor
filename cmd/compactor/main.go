// Command compactor rolls up per-event JSON objects in an S3-compatible
// bucket into day/week/month JSONL files, preserving client-side
// encryption end-to-end.
package main

//go:generate go run . man -o ../../docs/man/compactor.1
//go:generate go run . completions bash -o ../../docs/completions/compactor.bash
//go:generate go run . completions zsh -o ../../docs/completions/_compactor
//go:generate go run . completions fish -o ../../docs/completions/compactor.fish

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alecthomas/kong"
	kongyaml "github.com/alecthomas/kong-yaml"

	"github.com/pflege-de-labs/compactor/internal/cli"
	"github.com/pflege-de-labs/compactor/internal/config"
)

var version = "dev"

func main() {
	var root cli.CLI

	var configPaths []string
	if p, err := config.DefaultPath(); err == nil {
		configPaths = append(configPaths, p)
	}

	// SIGTERM/SIGINT cancel ctx so `compactor listen` (a long-lived
	// Deployment) can drain and exit cleanly instead of being killed.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// *cli.App is bootstrapped lazily, on first use, via a singleton
	// provider rather than unconditionally up front: `man` and
	// `completions` don't touch S3/encryption/etc at all and must work
	// without any of that configured (e.g. a fresh install generating
	// its own shell completions before ever touching a bucket).
	var app *cli.App
	kctx := kong.Parse(&root,
		kong.Name("compactor"),
		kong.Description("Roll up S3 event objects into day/week/month JSONL, preserving client-side encryption."),
		kong.UsageOnError(),
		kong.Vars{"version": version},
		kong.Configuration(kongyaml.Loader, configPaths...),
		// context.Context is an interface; Run()'s positional binds key
		// by concrete runtime type, which a signal.NotifyContext value
		// never matches against a `ctx context.Context` parameter. Bind
		// it explicitly against the interface type instead.
		kong.BindFor(ctx),
		kong.BindSingletonProvider(func() (*cli.App, error) {
			a, err := cli.Bootstrap(ctx, &root)
			app = a
			return a, err
		}),
	)

	runErr := kctx.Run()

	// Metrics delivery failing must never mask (or be masked by) the
	// command's own result, and must never block process exit. Only
	// runs if the command actually bootstrapped an App (see above).
	if app != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := app.Shutdown(shutdownCtx); err != nil {
			app.Logger.Warn("failed to flush metrics on shutdown", "error", err)
		}
		cancel()
	}

	kctx.FatalIfErrorf(runErr)
}
