package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/adrg/xdg"

	"github.com/pflege-de-labs/compactor/internal/materialize"
)

type MaterializeCmd struct {
	Key  string `arg:"" help:"Object key of the rollup to decrypt."`
	Out  string `help:"Scratch directory to write the decrypted file into (default: XDG cache dir)." type:"path"`
	Keep bool   `help:"Skip sweeping expired scratch files from a previous run before materializing."`
}

func (c *MaterializeCmd) Run(ctx context.Context, app *App) error {
	outDir := c.Out
	if outDir == "" {
		outDir = filepath.Join(xdg.CacheHome, "compactor", "materialized")
	}

	if !c.Keep {
		if err := materialize.SweepExpired(outDir, app.Config.Materialize.CacheTTL); err != nil {
			app.Logger.Warn("failed to sweep expired scratch files", "dir", outDir, "error", err)
		}
	}

	path, _, err := materialize.Materialize(ctx, app.Store, app.Registry, c.Key, outDir)
	if err != nil {
		return err
	}

	// The materialized file must still exist when this process exits —
	// callers typically use it via command substitution, e.g.
	// duckdb -c "... read_ndjson_auto('$(compactor materialize <key>)')".
	// It is reclaimed by the next run's expiry sweep, not by this one.
	fmt.Println(path)
	return nil
}
