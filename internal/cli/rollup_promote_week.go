package cli

import (
	"context"
	"fmt"
	"time"
)

type RollupPromoteWeekCmd struct {
	Week string `help:"ISO week to promote (YYYY-Www); default: current ISO week." optional:""`
}

func (c *RollupPromoteWeekCmd) Run(ctx context.Context, app *App) error {
	isoYear, isoWeek, err := c.target()
	if err != nil {
		return err
	}

	start := time.Now()
	result, err := app.Engine.RunPromoteWeek(ctx, isoYear, isoWeek)
	app.Metrics.RecordPromotion(ctx, "week", time.Since(start), result.NoOp, err)
	if err != nil {
		return fmt.Errorf("rollup promote-week %04d-W%02d: %w", isoYear, isoWeek, err)
	}

	if result.NoOp {
		app.Logger.Info("week rollup: unchanged", "week", fmt.Sprintf("%04d-W%02d", isoYear, isoWeek))
		return nil
	}
	app.Logger.Info("week rollup updated",
		"week", fmt.Sprintf("%04d-W%02d", isoYear, isoWeek),
		"key", result.RollupKey,
	)
	return nil
}

func (c *RollupPromoteWeekCmd) target() (isoYear, isoWeek int, err error) {
	if c.Week == "" {
		isoYear, isoWeek = time.Now().UTC().ISOWeek()
		return isoYear, isoWeek, nil
	}
	if _, err := fmt.Sscanf(c.Week, "%04d-W%02d", &isoYear, &isoWeek); err != nil {
		return 0, 0, fmt.Errorf("rollup promote-week: invalid --week %q (want YYYY-Www): %w", c.Week, err)
	}
	return isoYear, isoWeek, nil
}
