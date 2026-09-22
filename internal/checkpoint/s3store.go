package checkpoint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/pflege-de-labs/compactor/internal/storage"
)

// S3Store persists checkpoints as JSON objects on an
// storage.ObjectStore, reusing the same conditional-PUT machinery used
// for rollup objects.
type S3Store struct {
	Store  storage.ObjectStore
	Prefix string // e.g. "checkpoints"
}

func NewS3Store(store storage.ObjectStore, prefix string) *S3Store {
	return &S3Store{Store: store, Prefix: prefix}
}

func (s *S3Store) dayKey(day time.Time) string {
	return fmt.Sprintf("%s/day/%s.json", s.Prefix, day.Format("2006-01-02"))
}

func (s *S3Store) weekKey(isoYear, isoWeek int) string {
	return fmt.Sprintf("%s/week/%04d-W%02d.json", s.Prefix, isoYear, isoWeek)
}

func (s *S3Store) monthKey(year int, month time.Month) string {
	return fmt.Sprintf("%s/month/%04d-%02d.json", s.Prefix, year, int(month))
}

func load[T any](ctx context.Context, store storage.ObjectStore, key string) (T, string, error) {
	var zero T

	body, meta, err := store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return zero, "", nil
		}
		return zero, "", fmt.Errorf("checkpoint: load %s: %w", key, err)
	}
	defer body.Close()

	var out T
	if err := json.NewDecoder(body).Decode(&out); err != nil {
		return zero, "", fmt.Errorf("checkpoint: decode %s: %w", key, err)
	}
	return out, meta.ETag, nil
}

func save[T any](ctx context.Context, store storage.ObjectStore, key string, cp T, ifMatchETag string) (string, error) {
	data, err := json.Marshal(cp)
	if err != nil {
		return "", fmt.Errorf("checkpoint: encode %s: %w", key, err)
	}

	opts := storage.PutOptions{ContentType: "application/json"}
	if ifMatchETag == "" {
		opts.IfNoneMatch = "*"
	} else {
		opts.IfMatchETag = ifMatchETag
	}

	meta, err := store.Put(ctx, key, bytes.NewReader(data), opts)
	if err != nil {
		if errors.Is(err, storage.ErrPreconditionFailed) {
			return "", storage.ErrPreconditionFailed
		}
		return "", fmt.Errorf("checkpoint: save %s: %w", key, err)
	}
	return meta.ETag, nil
}

func (s *S3Store) LoadDay(ctx context.Context, day time.Time) (DayCheckpoint, string, error) {
	cp, etag, err := load[DayCheckpoint](ctx, s.Store, s.dayKey(day))
	if cp.ProcessedKeys == nil {
		cp.ProcessedKeys = make(map[string]bool)
	}
	return cp, etag, err
}

func (s *S3Store) SaveDay(ctx context.Context, cp DayCheckpoint, ifMatchETag string) (string, error) {
	cp.UpdatedAt = time.Now().UTC()
	cp.SchemaVersion = 1
	return save(ctx, s.Store, s.dayKey(parseDayPrefix(cp.Prefix)), cp, ifMatchETag)
}

func (s *S3Store) LoadWeek(ctx context.Context, isoYear, isoWeek int) (WeekCheckpoint, string, error) {
	cp, etag, err := load[WeekCheckpoint](ctx, s.Store, s.weekKey(isoYear, isoWeek))
	if cp.IncludedDays == nil {
		cp.IncludedDays = make(map[string]string)
	}
	return cp, etag, err
}

func (s *S3Store) SaveWeek(ctx context.Context, cp WeekCheckpoint, ifMatchETag string) (string, error) {
	cp.UpdatedAt = time.Now().UTC()
	isoYear, isoWeek, err := parseWeekKey(cp.WeekKey)
	if err != nil {
		return "", err
	}
	return save(ctx, s.Store, s.weekKey(isoYear, isoWeek), cp, ifMatchETag)
}

func (s *S3Store) LoadMonth(ctx context.Context, year int, month time.Month) (MonthCheckpoint, string, error) {
	cp, etag, err := load[MonthCheckpoint](ctx, s.Store, s.monthKey(year, month))
	if cp.IncludedWeeks == nil {
		cp.IncludedWeeks = make(map[string]string)
	}
	return cp, etag, err
}

func (s *S3Store) SaveMonth(ctx context.Context, cp MonthCheckpoint, ifMatchETag string) (string, error) {
	cp.UpdatedAt = time.Now().UTC()
	year, month, err := parseMonthKey(cp.MonthKey)
	if err != nil {
		return "", err
	}
	return save(ctx, s.Store, s.monthKey(year, month), cp, ifMatchETag)
}

func parseDayPrefix(prefix string) time.Time {
	t, _ := time.Parse("2006/01/02", prefix)
	return t
}

func parseWeekKey(weekKey string) (year, week int, err error) {
	if _, err := fmt.Sscanf(weekKey, "%04d-W%02d", &year, &week); err != nil {
		return 0, 0, fmt.Errorf("checkpoint: invalid week key %q: %w", weekKey, err)
	}
	return year, week, nil
}

func parseMonthKey(monthKey string) (year int, month time.Month, err error) {
	var m int
	if _, err := fmt.Sscanf(monthKey, "%04d-%02d", &year, &m); err != nil {
		return 0, 0, fmt.Errorf("checkpoint: invalid month key %q: %w", monthKey, err)
	}
	return year, time.Month(m), nil
}
