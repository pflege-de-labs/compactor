// Package rollup implements the core day/week/month rollup algorithm:
// fold new source events into a day's JSONL rollup, and rebuild
// week/month rollups from their already-consolidated children.
package rollup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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

// progressInterval throttles info-level progress logs so a large backlog shows life without flooding the log.
const progressInterval = 30 * time.Second

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
	log.Debug("discovering source objects", "prefix", sourcePrefix)
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
	hadExisting := cp.RollupETag != ""
	existingScheme := crypto.Scheme(cp.Scheme)
	if !hadExisting {
		existingScheme = crypto.SchemeNone
	}

	if len(newObjs) == 0 {
		// One HEAD per run catches a rollup changed or deleted after its checkpoint, e.g. by an overlapping run.
		matches, err := e.dayRollupMatches(ctx, day, cp.RollupETag, existingScheme)
		if err != nil {
			return Result{}, err
		}
		if matches {
			log.Debug("no new source events")
			return Result{NoOp: true}, nil
		}
	}

	var plaintext chunkBuffer
	existingKey, existingETag := "", ""
	if hadExisting {
		existingCodec, err := e.Registry.MustFor(existingScheme)
		if err != nil {
			return Result{}, fmt.Errorf("rollup: day %s: existing scheme: %w", dayPrefix, err)
		}
		existingKey = DayRollupKey(e.RollupPrefix, day, existingCodec)

		body, meta, err := e.Store.Get(ctx, existingKey)
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return Result{}, fmt.Errorf("rollup: get existing rollup %s: %w", existingKey, err)
		}
		if err == nil {
			defer body.Close()
			existingETag = meta.ETag
		}
		if err == nil && meta.ETag == cp.RollupETag {
			if err := existingCodec.Decrypt(ctx, &plaintext, body, existingKey); err != nil {
				return Result{}, fmt.Errorf("rollup: decrypt existing rollup %s: %w", existingKey, err)
			}
		} else {
			// A crash between rollup and checkpoint write, or a changed or deleted rollup:
			// appending would duplicate or drop events, so rebuild from every source object (issue #27).
			log.Warn("day rollup does not match its checkpoint, rebuilding from all source objects",
				"key", existingKey, "checkpoint_etag", cp.RollupETag, "rollup_etag", existingETag)
			newObjs = events
		}
	}
	sort.Slice(newObjs, func(i, j int) bool { return newObjs[i].Meta.Key < newObjs[j].Meta.Key })
	log.Info("day rollup: processing new source objects", "new", len(newObjs), "discovered", len(events))

	lastProgress := time.Now()
	progress := func(phase string, done int) {
		if time.Since(lastProgress) < progressInterval {
			return
		}
		lastProgress = time.Now()
		log.Info("day rollup progress", "phase", phase, "done", done, "total", len(newObjs))
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
		// Decoding ciphertext as plaintext would mark the object processed and write garbage, so fail before touching anything.
		if scheme == crypto.SchemeNone && crypto.LooksEncrypted(ev.Meta.Key) {
			return Result{}, fmt.Errorf("rollup: %s is encrypted but no codec for its scheme is configured (missing decryption key?)", ev.Meta.Key)
		}
		detected[i] = detectedEvent{event: ev, scheme: scheme}
		if scheme != crypto.SchemeNone {
			anyNewEncrypted = true
		}
		progress("detect", i+1)
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

	appended, skippedBad := 0, 0
	for i, d := range detected {
		progress("decode", i)
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
		log.Debug("processed source object", "object", d.event.Meta.Key, "scheme", d.scheme, "events", good, "bad_lines", bad)
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

	body, err := encode(ctx, outputCodec, &plaintext)
	if err != nil {
		return Result{}, fmt.Errorf("rollup: encrypt rollup %s: %w", rollupKey, err)
	}

	// A scheme upgrade (plaintext day gaining an encrypted event) targets a new key; the old one is left in place.
	knownETag := ""
	if existingKey == rollupKey {
		knownETag = existingETag
	}
	putOpts, err := e.overwriteOpts(ctx, rollupKey, knownETag)
	if err != nil {
		return Result{}, err
	}

	meta, err := e.Store.Put(ctx, rollupKey, body, putOpts)
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

// dayRollupMatches reports whether the day rollup still has the ETag its checkpoint recorded; no recorded rollup matches trivially.
func (e *Engine) dayRollupMatches(ctx context.Context, day time.Time, etag string, scheme crypto.Scheme) (bool, error) {
	if etag == "" {
		return true, nil
	}
	codec, err := e.Registry.MustFor(scheme)
	if err != nil {
		return false, fmt.Errorf("rollup: day %s: existing scheme: %w", DayPrefix(day), err)
	}
	key := DayRollupKey(e.RollupPrefix, day, codec)
	head, err := e.Store.Head(ctx, key)
	switch {
	case err == nil:
		return head.ETag == etag, nil
	case errors.Is(err, storage.ErrNotFound):
		return false, nil
	default:
		return false, fmt.Errorf("rollup: head %s: %w", key, err)
	}
}

// overwriteOpts conditions a rollup write on the object at key: If-Match on knownETag,
// else on whatever a HEAD finds, else create-only. An object the checkpoint doesn't
// know comes from a run that crashed before its checkpoint write; the caller's content
// covers that run's, so overwriting it is safe (issue #27).
func (e *Engine) overwriteOpts(ctx context.Context, key, knownETag string) (storage.PutOptions, error) {
	opts := storage.PutOptions{ContentType: "application/x-ndjson", IfMatchETag: knownETag}
	if knownETag != "" {
		return opts, nil
	}
	head, err := e.Store.Head(ctx, key)
	switch {
	case err == nil:
		opts.IfMatchETag = head.ETag
	case errors.Is(err, storage.ErrNotFound):
		opts.IfNoneMatch = "*"
	default:
		return opts, fmt.Errorf("rollup: head %s: %w", key, err)
	}
	return opts, nil
}

// encode returns the rollup body; plaintext output skips the codec's copy, which doubled peak memory on large days.
func encode(ctx context.Context, codec crypto.Codec, plaintext *chunkBuffer) (io.Reader, error) {
	if codec.Scheme() == crypto.SchemeNone {
		return plaintext.Reader(), nil
	}
	var out chunkBuffer
	if err := codec.Encrypt(ctx, &out, plaintext.Reader()); err != nil {
		return nil, err
	}
	return out.Reader(), nil
}

func joinPrefix(a, b string) string {
	if a == "" {
		return b
	}
	return a + "/" + b
}
