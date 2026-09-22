package daemon_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pflege-de-labs/compactor/internal/daemon"
)

func TestHealth_LivenessAlwaysOK_ReadinessTracksState(t *testing.T) {
	h := &daemon.Health{}
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()

	assertStatus(t, srv.URL+"/healthz", http.StatusOK)
	assertStatus(t, srv.URL+"/readyz", http.StatusServiceUnavailable)

	h.SetReady(true)
	assertStatus(t, srv.URL+"/healthz", http.StatusOK)
	assertStatus(t, srv.URL+"/readyz", http.StatusOK)

	h.SetReady(false)
	assertStatus(t, srv.URL+"/readyz", http.StatusServiceUnavailable)
}

func assertStatus(t *testing.T, url string, want int) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		t.Fatalf("GET %s: status = %d, want %d", url, resp.StatusCode, want)
	}
}
