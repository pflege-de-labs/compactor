package rollup_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/pflege-de-labs/compactor/internal/checkpoint"
	"github.com/pflege-de-labs/compactor/internal/storage"
)

// crashStore fails the next checkpoint write, like a crash between the rollup and checkpoint writes,
// and can hide keys to simulate a deleted rollup.
type crashStore struct {
	storage.ObjectStore
	failCheckpoint bool
	hidden         map[string]bool
}

var errCrash = errors.New("simulated crash")

func (s *crashStore) Put(ctx context.Context, key string, body io.Reader, opts storage.PutOptions) (storage.ObjectMeta, error) {
	if s.failCheckpoint && strings.HasPrefix(key, "checkpoints/") {
		s.failCheckpoint = false
		return storage.ObjectMeta{}, errCrash
	}
	if s.hidden[key] {
		// A deleted key has nothing to match against.
		opts.IfMatchETag, opts.IfNoneMatch = "", ""
		delete(s.hidden, key)
	}
	return s.ObjectStore.Put(ctx, key, body, opts)
}

func (s *crashStore) Get(ctx context.Context, key string) (io.ReadCloser, storage.ObjectMeta, error) {
	if s.hidden[key] {
		return nil, storage.ObjectMeta{}, storage.ErrNotFound
	}
	return s.ObjectStore.Get(ctx, key)
}

func (s *crashStore) Head(ctx context.Context, key string) (storage.ObjectMeta, error) {
	if s.hidden[key] {
		return storage.ObjectMeta{}, storage.ErrNotFound
	}
	return s.ObjectStore.Head(ctx, key)
}

func newCrashEngine(t *testing.T) (*crashStore, func(day time.Time, name, body string), func(key string) []string, func(day time.Time) error, func(day time.Time) error) {
	t.Helper()
	engine, mem := newEngine(t, nil)
	store := &crashStore{ObjectStore: mem, hidden: map[string]bool{}}
	engine.Store = store
	engine.Checkpoints = checkpoint.NewS3Store(store, "checkpoints")
	ctx := context.Background()
	put := func(day time.Time, name, body string) { putSource(t, mem, day, name, []byte(body)) }
	lines := func(key string) []string {
		body, _, err := mem.Get(ctx, key)
		if err != nil {
			t.Fatalf("get %s: %v", key, err)
		}
		defer body.Close()
		data, _ := io.ReadAll(body)
		var out []string
		for _, l := range nonEmptyLines(data) {
			out = append(out, string(l))
		}
		return out
	}
	day := func(d time.Time) error { _, err := engine.RunHourlyDayRollup(ctx, d); return err }
	week := func(d time.Time) error { y, w := d.ISOWeek(); _, err := engine.RunPromoteWeek(ctx, y, w); return err }
	return store, put, lines, day, week
}

var crashDay = time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)

const crashDayKey = "rollups/day/2026/10/19.jsonl"

func TestCrashAfterRollupUpdate_RetryDoesNotDuplicate(t *testing.T) {
	store, put, lines, day, _ := newCrashEngine(t)
	put(crashDay, "a.json", `{"id":1}`)
	if err := day(crashDay); err != nil {
		t.Fatal(err)
	}
	put(crashDay, "b.json", `{"id":2}`)
	store.failCheckpoint = true
	if err := day(crashDay); !errors.Is(err, errCrash) {
		t.Fatalf("expected the simulated crash, got %v", err)
	}
	if err := day(crashDay); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got := lines(crashDayKey); len(got) != 2 {
		t.Fatalf("rollup = %q, want exactly the two events", got)
	}
}

func TestCrashAfterRollupCreate_RetrySucceeds(t *testing.T) {
	store, put, lines, day, _ := newCrashEngine(t)
	put(crashDay, "a.json", `{"id":1}`)
	store.failCheckpoint = true
	if err := day(crashDay); !errors.Is(err, errCrash) {
		t.Fatalf("expected the simulated crash, got %v", err)
	}
	if err := day(crashDay); err != nil {
		t.Fatalf("retry must not be blocked by the crashed run's object: %v", err)
	}
	if got := lines(crashDayKey); len(got) != 1 {
		t.Fatalf("rollup = %q, want one event", got)
	}
}

func TestDeletedDayRollup_RebuiltFromAllSources(t *testing.T) {
	store, put, lines, day, _ := newCrashEngine(t)
	put(crashDay, "a.json", `{"id":1}`)
	if err := day(crashDay); err != nil {
		t.Fatal(err)
	}
	store.hidden[crashDayKey] = true
	put(crashDay, "b.json", `{"id":2}`)
	if err := day(crashDay); err != nil {
		t.Fatal(err)
	}
	if got := lines(crashDayKey); len(got) != 2 {
		t.Fatalf("rollup = %q, want both events", got)
	}
}

func TestCrashAfterWeekCreate_RetrySucceeds(t *testing.T) {
	store, put, lines, day, week := newCrashEngine(t)
	put(crashDay, "a.json", `{"id":1}`)
	if err := day(crashDay); err != nil {
		t.Fatal(err)
	}
	store.failCheckpoint = true
	if err := week(crashDay); !errors.Is(err, errCrash) {
		t.Fatalf("expected the simulated crash, got %v", err)
	}
	if err := week(crashDay); err != nil {
		t.Fatalf("retry must not be blocked by the crashed run's object: %v", err)
	}
	if got := lines("rollups/week/2026-W43.jsonl"); len(got) != 1 {
		t.Fatalf("week rollup = %q, want one event", got)
	}
}
