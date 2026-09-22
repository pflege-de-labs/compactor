package rollup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/pflege-de-labs/compactor/internal/crypto"
	"github.com/pflege-de-labs/compactor/internal/storage"
)

// RunPromoteWeek rebuilds the ISO week's rollup from its day rollups
// (Monday-start, per ISO 8601 — the config's week_start is reserved for
// future non-ISO week support). Unlike the day level, this is a full
// rebuild from children each time, short-circuited when every present
// child is unchanged since the last run.
func (e *Engine) RunPromoteWeek(ctx context.Context, isoYear, isoWeek int) (Result, error) {
	weekKeyStr := fmt.Sprintf("%04d-W%02d", isoYear, isoWeek)
	days := daysInISOWeek(isoYear, isoWeek)

	weekCP, weekCPETag, err := e.Checkpoints.LoadWeek(ctx, isoYear, isoWeek)
	if err != nil {
		return Result{}, fmt.Errorf("rollup: load week checkpoint: %w", err)
	}
	weekCP.WeekKey = weekKeyStr

	included := make(map[string]string, len(days))
	scheme := crypto.SchemeNone
	schemeSet := false
	unchanged := true
	present := 0

	for _, d := range days {
		dayPrefix := DayPrefix(d)
		dayCP, _, err := e.Checkpoints.LoadDay(ctx, d)
		if err != nil {
			return Result{}, fmt.Errorf("rollup: load day checkpoint %s: %w", dayPrefix, err)
		}
		if dayCP.RollupETag == "" {
			continue // day not rolled up yet, include it once it is
		}

		if !schemeSet {
			scheme = crypto.Scheme(dayCP.Scheme)
			schemeSet = true
		} else if crypto.Scheme(dayCP.Scheme) != scheme {
			e.logger().Warn("mixed encryption scheme across days in week rollup, skipping cycle",
				"week", weekKeyStr, "day", dayPrefix)
			return Result{}, ErrMixedScheme
		}

		if weekCP.IncludedDays[dayPrefix] != dayCP.RollupETag {
			unchanged = false
		}
		included[dayPrefix] = dayCP.RollupETag
		present++
	}

	if present == 0 {
		return Result{NoOp: true}, nil
	}
	if unchanged && len(included) == len(weekCP.IncludedDays) {
		return Result{NoOp: true, RollupKey: weekCP.RollupETag}, nil
	}

	codec, err := e.Registry.MustFor(scheme)
	if err != nil {
		return Result{}, fmt.Errorf("rollup: week %s: %w", weekKeyStr, err)
	}

	var combined bytes.Buffer
	for _, d := range days {
		dayCP, _, err := e.Checkpoints.LoadDay(ctx, d)
		if err != nil {
			return Result{}, fmt.Errorf("rollup: load day checkpoint %s: %w", DayPrefix(d), err)
		}
		if dayCP.RollupETag == "" {
			continue
		}
		dayKey := DayRollupKey(e.RollupPrefix, d, codec)
		if err := e.decryptChildInto(ctx, codec, dayKey, &combined); err != nil {
			return Result{}, err
		}
	}

	rollupKey := WeekRollupKey(e.RollupPrefix, isoYear, isoWeek, codec)
	meta, err := e.encryptAndPutRollup(ctx, codec, rollupKey, &combined, weekCP.RollupETag)
	if err != nil {
		return Result{}, err
	}

	weekCP.IncludedDays = included
	weekCP.RollupETag = meta.ETag
	weekCP.Scheme = string(scheme)
	if _, err := e.Checkpoints.SaveWeek(ctx, weekCP, weekCPETag); err != nil {
		return Result{}, fmt.Errorf("rollup: save week checkpoint: %w", err)
	}

	return Result{RollupKey: rollupKey, RollupETag: meta.ETag}, nil
}

// RunPromoteMonth rebuilds the calendar month's rollup from the ISO
// weeks its days fall into, following the same rebuild-from-children,
// short-circuit-if-unchanged strategy as RunPromoteWeek.
func (e *Engine) RunPromoteMonth(ctx context.Context, year int, month time.Month) (Result, error) {
	monthKeyStr := fmt.Sprintf("%04d-%02d", year, int(month))
	weeks := weeksInMonth(year, month)

	monthCP, monthCPETag, err := e.Checkpoints.LoadMonth(ctx, year, month)
	if err != nil {
		return Result{}, fmt.Errorf("rollup: load month checkpoint: %w", err)
	}
	monthCP.MonthKey = monthKeyStr

	included := make(map[string]string, len(weeks))
	scheme := crypto.SchemeNone
	schemeSet := false
	unchanged := true
	present := 0

	for _, w := range weeks {
		weekKeyStr := fmt.Sprintf("%04d-W%02d", w.year, w.week)
		weekCP, _, err := e.Checkpoints.LoadWeek(ctx, w.year, w.week)
		if err != nil {
			return Result{}, fmt.Errorf("rollup: load week checkpoint %s: %w", weekKeyStr, err)
		}
		if weekCP.RollupETag == "" {
			continue // week not rolled up yet, include it once it is
		}

		if !schemeSet {
			scheme = crypto.Scheme(weekCP.Scheme)
			schemeSet = true
		} else if crypto.Scheme(weekCP.Scheme) != scheme {
			e.logger().Warn("mixed encryption scheme across weeks in month rollup, skipping cycle",
				"month", monthKeyStr, "week", weekKeyStr)
			return Result{}, ErrMixedScheme
		}

		if monthCP.IncludedWeeks[weekKeyStr] != weekCP.RollupETag {
			unchanged = false
		}
		included[weekKeyStr] = weekCP.RollupETag
		present++
	}

	if present == 0 {
		return Result{NoOp: true}, nil
	}
	if unchanged && len(included) == len(monthCP.IncludedWeeks) {
		return Result{NoOp: true, RollupKey: monthCP.RollupETag}, nil
	}

	codec, err := e.Registry.MustFor(scheme)
	if err != nil {
		return Result{}, fmt.Errorf("rollup: month %s: %w", monthKeyStr, err)
	}

	var combined bytes.Buffer
	for _, w := range weeks {
		weekCP, _, err := e.Checkpoints.LoadWeek(ctx, w.year, w.week)
		if err != nil {
			return Result{}, fmt.Errorf("rollup: load week checkpoint %04d-W%02d: %w", w.year, w.week, err)
		}
		if weekCP.RollupETag == "" {
			continue
		}
		weekKey := WeekRollupKey(e.RollupPrefix, w.year, w.week, codec)
		if err := e.decryptChildInto(ctx, codec, weekKey, &combined); err != nil {
			return Result{}, err
		}
	}

	rollupKey := MonthRollupKey(e.RollupPrefix, year, month, codec)
	meta, err := e.encryptAndPutRollup(ctx, codec, rollupKey, &combined, monthCP.RollupETag)
	if err != nil {
		return Result{}, err
	}

	monthCP.IncludedWeeks = included
	monthCP.RollupETag = meta.ETag
	monthCP.Scheme = string(scheme)
	if _, err := e.Checkpoints.SaveMonth(ctx, monthCP, monthCPETag); err != nil {
		return Result{}, fmt.Errorf("rollup: save month checkpoint: %w", err)
	}

	return Result{RollupKey: rollupKey, RollupETag: meta.ETag}, nil
}

func (e *Engine) decryptChildInto(ctx context.Context, codec crypto.Codec, key string, into *bytes.Buffer) error {
	body, _, err := e.Store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("rollup: get child %s: %w", key, err)
	}
	defer body.Close()
	if err := codec.Decrypt(ctx, into, body, key); err != nil {
		return fmt.Errorf("rollup: decrypt child %s: %w", key, err)
	}
	return nil
}

// encryptAndPutRollup encrypts plaintext and writes it to key,
// conditioned on the rollup already existing (hadPrior=true, via
// If-Match against the object's current ETag) or not (create-only).
func (e *Engine) encryptAndPutRollup(ctx context.Context, codec crypto.Codec, key string, plaintext *bytes.Buffer, priorETag string) (storage.ObjectMeta, error) {
	var ciphertext bytes.Buffer
	if err := codec.Encrypt(ctx, &ciphertext, plaintext); err != nil {
		return storage.ObjectMeta{}, fmt.Errorf("rollup: encrypt %s: %w", key, err)
	}

	putOpts := storage.PutOptions{ContentType: "application/x-ndjson"}
	if priorETag != "" {
		existing, err := e.Store.Head(ctx, key)
		switch {
		case err == nil:
			putOpts.IfMatchETag = existing.ETag
		case errors.Is(err, storage.ErrNotFound):
			putOpts.IfNoneMatch = "*"
		default:
			return storage.ObjectMeta{}, fmt.Errorf("rollup: head %s: %w", key, err)
		}
	} else {
		putOpts.IfNoneMatch = "*"
	}

	meta, err := e.Store.Put(ctx, key, &ciphertext, putOpts)
	if err != nil {
		if errors.Is(err, storage.ErrPreconditionFailed) {
			e.logger().Warn("rollup write lost race, deferring to next scheduled run", "key", key)
			return storage.ObjectMeta{}, storage.ErrPreconditionFailed
		}
		return storage.ObjectMeta{}, fmt.Errorf("rollup: put %s: %w", key, err)
	}
	return meta, nil
}
