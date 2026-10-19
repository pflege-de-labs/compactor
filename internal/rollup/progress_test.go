package rollup_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/pflege-de-labs/compactor/internal/storage"
)

// slowSourceStore makes every source read take 20s of synctest's fake clock.
type slowSourceStore struct{ storage.ObjectStore }

func (s slowSourceStore) Get(ctx context.Context, key string) (io.ReadCloser, storage.ObjectMeta, error) {
	if strings.HasPrefix(key, "source/") {
		time.Sleep(20 * time.Second)
	}
	return s.ObjectStore.Get(ctx, key)
}

func (s slowSourceStore) GetRange(ctx context.Context, key string, n int64) ([]byte, error) {
	if strings.HasPrefix(key, "source/") {
		time.Sleep(20 * time.Second)
	}
	return s.ObjectStore.GetRange(ctx, key, n)
}

func TestRunHourlyDayRollup_ProgressLogIsThrottled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		engine, store := newEngine(t, nil)
		engine.Store = slowSourceStore{store}
		var logs bytes.Buffer
		engine.Logger = slog.New(slog.NewJSONHandler(&logs, nil))

		day := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
		for i := range 4 {
			putSource(t, store, day, fmt.Sprintf("%d.json", i), []byte(`{"n":1}`))
		}
		if _, err := engine.RunHourlyDayRollup(context.Background(), day); err != nil {
			t.Fatal(err)
		}

		var got []string
		for line := range strings.Lines(logs.String()) {
			var rec struct {
				Msg   string
				Phase string
				Done  int
			}
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				t.Fatal(err)
			}
			if rec.Msg == "day rollup progress" {
				got = append(got, fmt.Sprintf("%s/%d", rec.Phase, rec.Done))
			}
		}
		// 20s per read against a 30s interval: every second object logs, and the timer carries across phases.
		want := []string{"detect/2", "detect/4", "decode/2"}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("progress logs = %v, want %v", got, want)
		}
	})
}
