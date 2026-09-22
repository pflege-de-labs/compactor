package storage_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/pflege-de-labs/compactor/internal/storage"
)

func TestMemStore_ConditionalPut(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemStore()

	// Create-only write succeeds when nothing exists yet...
	meta, err := store.Put(ctx, "k", bytes.NewReader([]byte("v1")), storage.PutOptions{IfNoneMatch: "*"})
	if err != nil {
		t.Fatalf("create-only put: %v", err)
	}

	// ...and fails once the key exists.
	if _, err := store.Put(ctx, "k", bytes.NewReader([]byte("v2")), storage.PutOptions{IfNoneMatch: "*"}); !errors.Is(err, storage.ErrPreconditionFailed) {
		t.Fatalf("expected ErrPreconditionFailed on repeat create-only put, got %v", err)
	}

	// Update with the correct ETag succeeds...
	updated, err := store.Put(ctx, "k", bytes.NewReader([]byte("v2")), storage.PutOptions{IfMatchETag: meta.ETag})
	if err != nil {
		t.Fatalf("if-match put: %v", err)
	}

	// ...but a stale ETag (simulating a losing writer in a race) fails.
	if _, err := store.Put(ctx, "k", bytes.NewReader([]byte("v3")), storage.PutOptions{IfMatchETag: meta.ETag}); !errors.Is(err, storage.ErrPreconditionFailed) {
		t.Fatalf("expected ErrPreconditionFailed on stale if-match put, got %v", err)
	}

	body, _, err := store.Get(ctx, "k")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer body.Close()
	data := make([]byte, 2)
	if _, err := body.Read(data); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "v2" {
		t.Fatalf("expected winning write v2 to stick, got %q", data)
	}
	_ = updated
}

func TestMemStore_GetMissingReturnsErrNotFound(t *testing.T) {
	store := storage.NewMemStore()
	if _, _, err := store.Get(context.Background(), "missing"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
