package cli

import (
	"context"
	"fmt"
	"time"
)

type RollupPromoteMonthCmd struct {
	Month string `help:"Month to promote (YYYY-MM); default: current month." optional:""`
}

func (c *RollupPromoteMonthCmd) Run(ctx context.Context, app *App) error {
	year, month, err := c.target()
	if err != nil {
		return err
	}

	start := time.Now()
	result, err := app.Engine.RunPromoteMonth(ctx, year, month)
	app.Metrics.RecordPromotion(ctx, "month", time.Since(start), result.NoOp, err)
	if err != nil {
		return fmt.Errorf("rollup promote-month %04d-%02d: %w", year, int(month), err)
	}

	if result.NoOp {
		app.Logger.Info("month rollup: unchanged", "month", fmt.Sprintf("%04d-%02d", year, int(month)))
		return nil
	}
	app.Logger.Info("month rollup updated",
		"month", fmt.Sprintf("%04d-%02d", year, int(month)),
		"key", result.RollupKey,
	)
	return nil
}

func (c *RollupPromoteMonthCmd) target() (year int, month time.Month, err error) {
	if c.Month == "" {
		now := time.Now().UTC()
		return now.Year(), now.Month(), nil
	}
	var m int
	if _, err := fmt.Sscanf(c.Month, "%04d-%02d", &year, &m); err != nil {
		return 0, 0, fmt.Errorf("rollup promote-month: invalid --month %q (want YYYY-MM): %w", c.Month, err)
	}
	return year, time.Month(m), nil
}
