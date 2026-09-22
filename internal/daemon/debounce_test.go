package daemon_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/pflege-de-labs/compactor/internal/daemon"
)

func TestDebouncer_CoalescesBurstIntoOneTrigger(t *testing.T) {
	var calls atomic.Int32
	day := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)

	d := daemon.NewDebouncer(50*time.Millisecond, func(got time.Time) {
		if !got.Equal(day) {
			t.Errorf("trigger day = %v, want %v", got, day)
		}
		calls.Add(1)
	})

	for range 5 {
		d.Notify(day)
		time.Sleep(10 * time.Millisecond)
	}

	time.Sleep(150 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected exactly 1 coalesced trigger, got %d", got)
	}
}

func TestDebouncer_DifferentDaysTriggerIndependently(t *testing.T) {
	var calls atomic.Int32
	d := daemon.NewDebouncer(20*time.Millisecond, func(time.Time) {
		calls.Add(1)
	})

	d.Notify(time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC))
	d.Notify(time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC))

	time.Sleep(100 * time.Millisecond)
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected 2 independent triggers, got %d", got)
	}
}

func TestDebouncer_StopCancelsPendingTriggers(t *testing.T) {
	var calls atomic.Int32
	d := daemon.NewDebouncer(50*time.Millisecond, func(time.Time) {
		calls.Add(1)
	})

	d.Notify(time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC))
	d.Stop()

	time.Sleep(100 * time.Millisecond)
	if got := calls.Load(); got != 0 {
		t.Fatalf("expected 0 triggers after Stop, got %d", got)
	}
}
