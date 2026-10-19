package storage_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pflege-de-labs/compactor/internal/storage"
)

// fakeRGW mimics Ceph RGW: it rejects any PUT carrying If-Match, like the provider's backend.
func fakeRGW(t *testing.T, etag string) (*httptest.Server, *int) {
	puts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			w.Header().Set("ETag", etag)
		case http.MethodPut:
			if r.Header.Get("If-Match") != "" {
				w.WriteHeader(http.StatusPreconditionFailed)
				return
			}
			puts++
			w.Header().Set("ETag", `"new"`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &puts
}

func TestS3Store_IfMatchDisabled(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	ctx := context.Background()
	srv, puts := fakeRGW(t, `"current"`)
	store, err := storage.NewS3Store(ctx, storage.S3Config{Endpoint: srv.URL, Region: "us-east-1", Bucket: "b", UsePathStyle: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.Put(ctx, "k", bytes.NewReader([]byte("v")), storage.PutOptions{IfMatchETag: `"stale"`}); !errors.Is(err, storage.ErrPreconditionFailed) {
		t.Fatalf("stale ETag: expected ErrPreconditionFailed, got %v", err)
	}
	if *puts != 0 {
		t.Fatalf("stale ETag must not PUT, got %d PUTs", *puts)
	}

	meta, err := store.Put(ctx, "k", bytes.NewReader([]byte("v")), storage.PutOptions{IfMatchETag: `"current"`})
	if err != nil {
		t.Fatalf("matching ETag: %v", err)
	}
	if meta.ETag != `"new"` || *puts != 1 {
		t.Fatalf("matching ETag: got ETag %s after %d PUTs", meta.ETag, *puts)
	}
}
