package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/pflege-de-labs/compactor/internal/rollup"
	"github.com/pflege-de-labs/compactor/internal/storage"
)

type RollupBackfillCmd struct {
	From      string `required:"" help:"First day to roll up (YYYY-MM-DD)."`
	To        string `required:"" help:"Last day to roll up (YYYY-MM-DD), inclusive."`
	NoPromote bool   `name:"no-promote" help:"Only roll up days; skip week and month promotion, which holds a whole week or month in memory."`
}

func (c *RollupBackfillCmd) Run(ctx context.Context, app *App) error {
	days, weeks, months, err := backfillUnits(c.From, c.To)
	if err != nil {
		return err
	}
	app.Logger.Info("backfill started", "from", c.From, "to", c.To, "days", len(days), "weeks", len(weeks), "months", len(months))

	// Promote even after a deferred day so the rest of the range lands; a rerun picks up the missing day.
	firstErr := runDayRollups(ctx, app, days)
	if c.NoPromote {
		weeks, months = nil, nil
	}
	for _, w := range weeks {
		if err := (&RollupPromoteWeekCmd{Week: w}).Run(ctx, app); err != nil {
			if !deferrable(err) {
				return err
			}
			firstErr = firstOf(firstErr, err)
		}
	}
	for _, m := range months {
		if err := (&RollupPromoteMonthCmd{Month: m}).Run(ctx, app); err != nil {
			if !deferrable(err) {
				return err
			}
			firstErr = firstOf(firstErr, err)
		}
	}
	app.Logger.Info("backfill finished", "from", c.From, "to", c.To, "error", firstErr)
	return firstErr
}

// backfillUnits expands [from, to] into its days and the ISO weeks (YYYY-Www) and months (YYYY-MM) they touch, oldest first.
func backfillUnits(from, to string) (days []time.Time, weeks, months []string, err error) {
	start, err := time.Parse("2006-01-02", from)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("rollup backfill: invalid --from %q: %w", from, err)
	}
	end, err := time.Parse("2006-01-02", to)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("rollup backfill: invalid --to %q: %w", to, err)
	}
	if end.Before(start) {
		return nil, nil, nil, fmt.Errorf("rollup backfill: --to %s is before --from %s", to, from)
	}
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		days = append(days, d)
		y, w := d.ISOWeek()
		if week := fmt.Sprintf("%04d-W%02d", y, w); len(weeks) == 0 || weeks[len(weeks)-1] != week {
			weeks = append(weeks, week)
		}
		if month := d.Format("2006-01"); len(months) == 0 || months[len(months)-1] != month {
			months = append(months, month)
		}
	}
	return days, weeks, months, nil
}

func deferrable(err error) bool {
	return errors.Is(err, rollup.ErrMixedScheme) || errors.Is(err, storage.ErrPreconditionFailed)
}

func firstOf(first, err error) error {
	if first != nil {
		return first
	}
	return err
}
