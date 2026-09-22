package storage

import (
	"context"
	"time"
)

type SourceEvent struct {
	Meta ObjectMeta
}

// Cursor is an opaque discovery watermark. PollLister does not depend on
// keys being lexicographically time-sortable: it re-lists the full
// prefix each call and lets the caller (rollup.Engine) filter against
// its own checkpoint of already-processed keys. Cursor is kept for
// listers that can watermark cheaply (e.g. a future notification-based
// implementation).
type Cursor struct {
	LastKey      string
	LastModified time.Time
}

// Lister discovers source objects under prefix. PollLister (list +
// checkpoint-filter) is the only implementation today; a future
// push-based implementation (e.g. MinIO bucket notifications, run as a
// long-lived listener) can satisfy the same interface without any
// change to rollup.Engine.
type Lister interface {
	Discover(ctx context.Context, prefix string, since Cursor) ([]SourceEvent, Cursor, error)
}
