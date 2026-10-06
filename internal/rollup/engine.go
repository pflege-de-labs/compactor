// Package rollup implements the core day/week/month rollup algorithm:
// fold new source events into a day's JSONL rollup, and rebuild
// week/month rollups from their already-consolidated children.
package rollup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/pflege-de-labs/compactor/internal/checkpoint"
	"github.com/pflege-de-labs/compactor/internal/crypto"
	"github.com/pflege-de-labs/compactor/internal/storage"
)

// ErrMixedScheme is returned when a week/month's children were written
// with different output encryption schemes (e.g. the configured output
// scheme changed between when they were rolled up). compactor never
// silently downgrades confidentiality by treating a mismatch as
// plaintext, so the unit is skipped for this cycle instead.
var ErrMixedScheme = errors.New("rollup: mixed encryption scheme within one rollup unit")

// sniffLen is how many header bytes are read to detect a codec via
// Registry.Detect when a key's extension alone is inconclusive.
const sniffLen = 64

type Result struct {
	ItemsAppended int
	SkippedBad    int
	RollupKey     string
	RollupETag    string
	NoOp          bool
}

type Engine struct {
	Store       storage.ObjectStore
	Lister      storage.Lister
	Checkpoints checkpoint.Store
	// Registry holds every codec compactor can decode a source object
	// with (including source-decode-only codecs an upstream producer
	// uses but compactor itself never writes).
	Registry *crypto.Registry
	// OutputScheme is the single scheme compactor encrypts rollup
	// output with, independent of whichever scheme(s) contributing
	// source objects used. A day/week/month rollup is encrypted with
	// this scheme if any source event folded into it was encrypted (in
	// any scheme); otherwise it stays plaintext.
	OutputScheme crypto.Scheme

	// SourcePrefix is prepended to the year/month/day prefix to locate
	// raw event objects; RollupPrefix roots the day/week/month rollup
	// tree. The two must not overlap or discovery would try to fold
	// rollup output back in as a source event.
	SourcePrefix string
	RollupPrefix string

	Clock  func() time.Time
	Logger *slog.Logger
}

func (e *Engine) logger() *slog.Logger {
	if e.Logger != nil {
		return e.Logger
	}
	return slog.Default()
}

type detectedEvent struct {
	event  storage.SourceEvent
	scheme crypto.Scheme
}

// RunHourlyDayRollup folds any source events under day's prefix that
// aren't yet in the day's checkpoint into its JSONL rollup. Safe to call
// repeatedly (including concurrently across separate days): a
// conditional-PUT precondition failure aborts this call cleanly and the
// next scheduled run catches up.
func (e *Engine) RunHourlyDayRollup(ctx context.Context, day time.Time) (Result, error) {
	dayPrefix := DayPrefix(day)
	log := e.logger().With("day", dayPrefix)

	cp, cpETag, err := e.Checkpoints.LoadDay(ctx, day)
	if err != nil {
		return Result{}, fmt.Errorf("rollup: load day checkpoint: %w", err)
	}
	cp.Prefix = dayPrefix

	sourcePrefix := joinPrefix(e.SourcePrefix, dayPrefix)
	events, _, err := e.Lister.Discover(ctx, sourcePrefix, storage.Cursor{})
	if err != nil {
		return Result{}, fmt.Errorf("rollup: discover %s: %w", sourcePrefix, err)
	}

	var newObjs []storage.SourceEvent
	for _, ev := range events {
		if !cp.ProcessedKeys[ev.Meta.Key] {
			newObjs = append(newObjs, ev)
		}
	}
	if len(newObjs) == 0 {
		log.Debug("no new source events")
		return Result{NoOp: true}, nil
	}
	sort.Slice(newObjs, func(i, j int) bool { return newObjs[i].Meta.Key < newObjs[j].Meta.Key })

	hadExisting := cp.RollupETag != ""
	existingScheme := crypto.Scheme(cp.Scheme)
	if !hadExisting {
		existingScheme = crypto.SchemeNone
	}

	// Detect each new object's scheme independently — unlike source
	// objects, which may legitimately mix schemes (or mix encrypted and
	// plaintext) within one day, the rollup itself always gets a single
	// output scheme (see Engine.OutputScheme doc).
	detected := make([]detectedEvent, len(newObjs))
	anyNewEncrypted := false
	for i, ev := range newObjs {
		header, err := e.Store.GetRange(ctx, ev.Meta.Key, sniffLen)
		if err != nil {
			return Result{}, fmt.Errorf("rollup: sniff %s: %w", ev.Meta.Key, err)
		}
		scheme := e.Registry.Detect(ev.Meta.Key, header)
		detected[i] = detectedEvent{event: ev, scheme: scheme}
		if scheme != crypto.SchemeNone {
			anyNewEncrypted = true
		}
	}

	finalScheme := existingScheme
	if finalScheme == crypto.SchemeNone && anyNewEncrypted {
		finalScheme = e.OutputScheme
	}

	outputCodec, err := e.Registry.MustFor(finalScheme)
	if err != nil {
		return Result{}, fmt.Errorf("rollup: day %s: output scheme: %w", dayPrefix, err)
	}
	rollupKey := DayRollupKey(e.RollupPrefix, day, outputCodec)

	var plaintext bytes.Buffer
	existingETag := ""
	if hadExisting {
		existingCodec, err := e.Registry.MustFor(existingScheme)
		if err != nil {
			return Result{}, fmt.Errorf("rollup: day %s: existing scheme: %w", dayPrefix, err)
		}
		existingKey := DayRollupKey(e.RollupPrefix, day, existingCodec)

		body, meta, err := e.Store.Get(ctx, existingKey)
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return Result{}, fmt.Errorf("rollup: get existing rollup %s: %w", existingKey, err)
		}
		if err == nil {
			defer body.Close()
			if err := existingCodec.Decrypt(ctx, &plaintext, body, existingKey); err != nil {
				return Result{}, fmt.Errorf("rollup: decrypt existing rollup %s: %w", existingKey, err)
			}
			// Only reuse the ETag for a conditional update if the
			// output scheme hasn't changed since — a scheme upgrade
			// (plaintext day gaining an encrypted event) targets a new
			// key, created fresh; the old key is left in place.
			if existingKey == rollupKey {
				existingETag = meta.ETag
			}
		}
	}

	appended, skippedBad := 0, 0
	for _, d := range detected {
		srcCodec, err := e.Registry.MustFor(d.scheme)
		if err != nil {
			log.Warn("no codec for detected scheme, skipping object", "object", d.event.Meta.Key, "scheme", d.scheme, "error", err)
			skippedBad++
			cp.ProcessedKeys[d.event.Meta.Key] = true
			continue
		}

		body, _, err := e.Store.Get(ctx, d.event.Meta.Key)
		if err != nil {
			return Result{}, fmt.Errorf("rollup: get source %s: %w", d.event.Meta.Key, err)
		}
		var decoded bytes.Buffer
		decErr := srcCodec.Decrypt(ctx, &decoded, body, d.event.Meta.Key)
		_ = body.Close()
		if decErr != nil {
			log.Warn("failed to decrypt source event, skipping", "object", d.event.Meta.Key, "error", decErr)
			skippedBad++
			cp.ProcessedKeys[d.event.Meta.Key] = true
			continue
		}

		good, bad := AppendJSONLines(&plaintext, decoded.Bytes())
		appended += good
		skippedBad += bad
		cp.ProcessedKeys[d.event.Meta.Key] = true
	}

	if appended == 0 {
		// Every new object was bad, or decoded to zero valid events;
		// still persist the checkpoint so we don't retry the same
		// poison objects next cycle, but skip the rollup rewrite.
		if _, err := e.Checkpoints.SaveDay(ctx, cp, cpETag); err != nil {
			return Result{}, fmt.Errorf("rollup: save day checkpoint: %w", err)
		}
		return Result{SkippedBad: skippedBad, NoOp: true}, nil
	}

	var ciphertext bytes.Buffer
	if err := outputCodec.Encrypt(ctx, &ciphertext, &plaintext); err != nil {
		return Result{}, fmt.Errorf("rollup: encrypt rollup %s: %w", rollupKey, err)
	}

	putOpts := storage.PutOptions{ContentType: "application/x-ndjson"}
	if existingETag != "" {
		putOpts.IfMatchETag = existingETag
	} else {
		putOpts.IfNoneMatch = "*"
	}

	meta, err := e.Store.Put(ctx, rollupKey, &ciphertext, putOpts)
	if err != nil {
		if errors.Is(err, storage.ErrPreconditionFailed) {
			log.Warn("rollup write lost race, deferring to next scheduled run", "key", rollupKey)
			return Result{}, storage.ErrPreconditionFailed
		}
		return Result{}, fmt.Errorf("rollup: put rollup %s: %w", rollupKey, err)
	}

	cp.RollupETag = meta.ETag
	cp.Scheme = string(finalScheme)
	if _, err := e.Checkpoints.SaveDay(ctx, cp, cpETag); err != nil {
		return Result{}, fmt.Errorf("rollup: save day checkpoint: %w", err)
	}

	return Result{
		ItemsAppended: appended,
		SkippedBad:    skippedBad,
		RollupKey:     rollupKey,
		RollupETag:    meta.ETag,
	}, nil
}

func joinPrefix(a, b string) string {
	if a == "" {
		return b
	}
	return a + "/" + b
}
