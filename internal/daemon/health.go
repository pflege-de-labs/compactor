package daemon

import (
	"net/http"
	"sync/atomic"
)

// Health backs the Deployment's Kubernetes liveness/readiness probes.
// Liveness is unconditional (the process is up and serving); readiness
// reflects whether the notification subscriber is currently connected
// and consuming.
type Health struct {
	ready atomic.Bool
}

func (h *Health) SetReady(ready bool) { h.ready.Store(ready) }

func (h *Health) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if h.ready.Load() {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	return mux
}
