package daemon

import (
	"sync"
	"time"
)

// Debouncer coalesces bursts of notifications for the same day into a
// single trigger call, fired window after the last notification for
// that day. A day busy with many individual object-created events
// (e.g. a backfill) reconciles once per quiet period, not once per
// event.
type Debouncer struct {
	mu      sync.Mutex
	pending map[string]*time.Timer
	window  time.Duration
	trigger func(day time.Time)
}

func NewDebouncer(window time.Duration, trigger func(day time.Time)) *Debouncer {
	return &Debouncer{
		pending: make(map[string]*time.Timer),
		window:  window,
		trigger: trigger,
	}
}

func (d *Debouncer) Notify(day time.Time) {
	key := day.Format("2006-01-02")

	d.mu.Lock()
	defer d.mu.Unlock()

	if t, ok := d.pending[key]; ok {
		t.Reset(d.window)
		return
	}
	d.pending[key] = time.AfterFunc(d.window, func() {
		d.mu.Lock()
		delete(d.pending, key)
		d.mu.Unlock()
		d.trigger(day)
	})
}

// Stop cancels all pending timers without firing them, for shutdown.
func (d *Debouncer) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for key, t := range d.pending {
		t.Stop()
		delete(d.pending, key)
	}
}
