package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/pflege-de-labs/compactor/internal/rollup"
	"github.com/pflege-de-labs/compactor/internal/storage"
)

type RollupHourlyCmd struct {
	Day string `help:"Day to process (YYYY-MM-DD); default: today plus configured straggler days." optional:""`
}

func (c *RollupHourlyCmd) Run(ctx context.Context, app *App) error {
	days, err := c.days(app)
	if err != nil {
		return err
	}

	var firstErr error
	for _, day := range days {
		start := time.Now()
		result, err := app.Engine.RunHourlyDayRollup(ctx, day)
		app.Metrics.RecordDayRollup(ctx, time.Since(start), result.ItemsAppended, result.SkippedBad, result.NoOp, err)
		if err != nil {
			if errors.Is(err, rollup.ErrMixedScheme) || errors.Is(err, storage.ErrPreconditionFailed) {
				// Already logged by the engine; keep processing other days.
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			return fmt.Errorf("rollup hourly %s: %w", day.Format("2006-01-02"), err)
		}

		switch {
		case result.NoOp:
			app.Logger.Info("day rollup: no new events", "day", day.Format("2006-01-02"), "skipped_bad", result.SkippedBad)
		default:
			app.Logger.Info("day rollup updated",
				"day", day.Format("2006-01-02"),
				"appended", result.ItemsAppended,
				"skipped_bad", result.SkippedBad,
				"key", result.RollupKey,
			)
		}
	}
	return firstErr
}

func (c *RollupHourlyCmd) days(app *App) ([]time.Time, error) {
	if c.Day == "" {
		return rollup.DaysToProcess(app.Engine.Clock().UTC(), app.Config.Rollup.StragglerDays), nil
	}
	d, err := time.Parse("2006-01-02", c.Day)
	if err != nil {
		return nil, fmt.Errorf("rollup hourly: invalid --day %q: %w", c.Day, err)
	}
	return []time.Time{d}, nil
}
