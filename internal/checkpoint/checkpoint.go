// Package checkpoint tracks which source objects have already been
// folded into a rollup, and which child rollups have already been
// folded into their parent, so scheduled runs are idempotent and safe
// under overlap.
package checkpoint

import (
	"context"
	"time"
)

type DayCheckpoint struct {
	Prefix        string          `json:"prefix"` // "2026/09/22"
	ProcessedKeys map[string]bool `json:"processed_keys"`
	RollupETag    string          `json:"rollup_etag"`
	// Scheme is the crypto.Scheme (e.g. "none", "age") this day's rollup
	// is currently encrypted with, so the rollup key (which embeds the
	// codec's extension) can be located without re-detecting from scratch.
	Scheme        string    `json:"scheme"`
	SchemaVersion int       `json:"schema_version"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type WeekCheckpoint struct {
	WeekKey      string            `json:"week_key"`      // "2026-W39"
	IncludedDays map[string]string `json:"included_days"` // day prefix -> day rollup ETag folded in
	RollupETag   string            `json:"rollup_etag"`
	Scheme       string            `json:"scheme"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

type MonthCheckpoint struct {
	MonthKey      string            `json:"month_key"` // "2026-09"
	IncludedWeeks map[string]string `json:"included_weeks"`
	RollupETag    string            `json:"rollup_etag"`
	Scheme        string            `json:"scheme"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

// Store persists checkpoints as small JSON objects, conditioned on the
// caller-supplied ETag (empty = create-only) so concurrent/overlapping
// runs fail loudly instead of silently clobbering each other. Callers
// must save the rollup object itself before saving its checkpoint: if a
// run crashes in between, the next run just redoes an idempotent merge
// rather than losing events (see docs/adr/0004).
type Store interface {
	LoadDay(ctx context.Context, day time.Time) (cp DayCheckpoint, etag string, err error)
	SaveDay(ctx context.Context, cp DayCheckpoint, ifMatchETag string) (newETag string, err error)

	LoadWeek(ctx context.Context, isoYear, isoWeek int) (cp WeekCheckpoint, etag string, err error)
	SaveWeek(ctx context.Context, cp WeekCheckpoint, ifMatchETag string) (newETag string, err error)

	LoadMonth(ctx context.Context, year int, month time.Month) (cp MonthCheckpoint, etag string, err error)
	SaveMonth(ctx context.Context, cp MonthCheckpoint, ifMatchETag string) (newETag string, err error)
}
