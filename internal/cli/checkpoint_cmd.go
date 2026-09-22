package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type CheckpointCmd struct {
	Show  CheckpointShowCmd  `cmd:"" help:"Print a checkpoint's current state as JSON."`
	Reset CheckpointResetCmd `cmd:"" help:"Clear a checkpoint's tracking state (operator escape hatch)."`
}

type checkpointTarget struct {
	Day   string `help:"Day checkpoint to target (YYYY-MM-DD)." xor:"target"`
	Week  string `help:"Week checkpoint to target (YYYY-Www)." xor:"target"`
	Month string `help:"Month checkpoint to target (YYYY-MM)." xor:"target"`
}

func (t checkpointTarget) parse() (kind string, day time.Time, isoYear, isoWeek, year int, month time.Month, err error) {
	switch {
	case t.Day != "":
		day, err = time.Parse("2006-01-02", t.Day)
		return "day", day, 0, 0, 0, 0, err
	case t.Week != "":
		if _, err = fmt.Sscanf(t.Week, "%04d-W%02d", &isoYear, &isoWeek); err != nil {
			return "", time.Time{}, 0, 0, 0, 0, fmt.Errorf("invalid --week %q: %w", t.Week, err)
		}
		return "week", time.Time{}, isoYear, isoWeek, 0, 0, nil
	case t.Month != "":
		var m int
		if _, err = fmt.Sscanf(t.Month, "%04d-%02d", &year, &m); err != nil {
			return "", time.Time{}, 0, 0, 0, 0, fmt.Errorf("invalid --month %q: %w", t.Month, err)
		}
		return "month", time.Time{}, 0, 0, year, time.Month(m), nil
	default:
		return "", time.Time{}, 0, 0, 0, 0, fmt.Errorf("exactly one of --day, --week, --month is required")
	}
}

type CheckpointShowCmd struct {
	checkpointTarget
}

func (c *CheckpointShowCmd) Run(ctx context.Context, app *App) error {
	kind, day, isoYear, isoWeek, year, month, err := c.parse()
	if err != nil {
		return err
	}

	var v any
	switch kind {
	case "day":
		v, _, err = app.Checkpoints.LoadDay(ctx, day)
	case "week":
		v, _, err = app.Checkpoints.LoadWeek(ctx, isoYear, isoWeek)
	case "month":
		v, _, err = app.Checkpoints.LoadMonth(ctx, year, month)
	}
	if err != nil {
		return fmt.Errorf("checkpoint show: %w", err)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

type CheckpointResetCmd struct {
	checkpointTarget
	Confirm bool `help:"Required to actually perform the reset." default:"false"`
}

// Run clears a checkpoint's tracking state only. It does not delete the
// underlying rollup object: after a reset, the next scheduled run's
// create-only write will fail with a precondition error until the
// stale rollup object is also removed out-of-band. This is a deliberate
// operator escape hatch for recovering from a corrupted checkpoint, not
// a routine operation.
func (c *CheckpointResetCmd) Run(ctx context.Context, app *App) error {
	if !c.Confirm {
		return fmt.Errorf("checkpoint reset: refusing to proceed without --confirm")
	}

	kind, day, isoYear, isoWeek, year, month, err := c.parse()
	if err != nil {
		return err
	}

	app.Logger.Warn("resetting checkpoint tracking state; this does NOT delete the underlying rollup object")

	switch kind {
	case "day":
		cp, etag, err := app.Checkpoints.LoadDay(ctx, day)
		if err != nil {
			return fmt.Errorf("checkpoint reset: %w", err)
		}
		cp.ProcessedKeys = nil
		cp.RollupETag = ""
		cp.Scheme = ""
		_, err = app.Checkpoints.SaveDay(ctx, cp, etag)
		return err
	case "week":
		cp, etag, err := app.Checkpoints.LoadWeek(ctx, isoYear, isoWeek)
		if err != nil {
			return fmt.Errorf("checkpoint reset: %w", err)
		}
		cp.IncludedDays = nil
		cp.RollupETag = ""
		cp.Scheme = ""
		_, err = app.Checkpoints.SaveWeek(ctx, cp, etag)
		return err
	case "month":
		cp, etag, err := app.Checkpoints.LoadMonth(ctx, year, month)
		if err != nil {
			return fmt.Errorf("checkpoint reset: %w", err)
		}
		cp.IncludedWeeks = nil
		cp.RollupETag = ""
		cp.Scheme = ""
		_, err = app.Checkpoints.SaveMonth(ctx, cp, etag)
		return err
	}
	return nil
}
