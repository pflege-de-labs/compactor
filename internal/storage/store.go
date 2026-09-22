// Package storage abstracts the S3-compatible object store so rollup
// logic never depends on a concrete SDK.
package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

var (
	ErrNotFound           = errors.New("storage: object not found")
	ErrPreconditionFailed = errors.New("storage: precondition failed")
)

type ObjectMeta struct {
	Key          string
	Size         int64
	ETag         string
	LastModified time.Time
}

// PutOptions applies an optimistic-concurrency precondition to a write.
// At most one of IfMatchETag or IfNoneMatch should be set.
type PutOptions struct {
	// IfMatchETag requires the existing object to have this ETag; the
	// write fails with ErrPreconditionFailed otherwise. Use for updates.
	IfMatchETag string
	// IfNoneMatch, set to "*", requires that no object currently exists
	// at the key. Use for create-only writes.
	IfNoneMatch string
	ContentType string
}

// ObjectStore is the minimal S3-shaped surface compactor needs. The S3
// implementation supports custom endpoints and path-style addressing so
// it also works against MinIO.
type ObjectStore interface {
	List(ctx context.Context, prefix string) ([]ObjectMeta, error)
	Get(ctx context.Context, key string) (io.ReadCloser, ObjectMeta, error)
	// GetRange reads at most the first n bytes, for magic-byte sniffing
	// without downloading the whole object.
	GetRange(ctx context.Context, key string, n int64) ([]byte, error)
	Put(ctx context.Context, key string, body io.Reader, opts PutOptions) (ObjectMeta, error)
	Head(ctx context.Context, key string) (ObjectMeta, error)
}
