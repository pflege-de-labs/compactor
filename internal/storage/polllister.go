package storage

import "context"

// PollLister discovers new objects by re-listing the full prefix on
// every call. It intentionally does not trust key ordering or Cursor for
// filtering — rollup.Engine cross-references the result against its own
// checkpoint of processed keys, which is robust regardless of how
// producers name their objects.
type PollLister struct {
	Store ObjectStore
}

func NewPollLister(store ObjectStore) *PollLister {
	return &PollLister{Store: store}
}

func (l *PollLister) Discover(ctx context.Context, prefix string, since Cursor) ([]SourceEvent, Cursor, error) {
	metas, err := l.Store.List(ctx, prefix)
	if err != nil {
		return nil, since, err
	}

	events := make([]SourceEvent, len(metas))
	cursor := since
	for i, m := range metas {
		events[i] = SourceEvent{Meta: m}
		if m.Key > cursor.LastKey {
			cursor.LastKey = m.Key
		}
		if m.LastModified.After(cursor.LastModified) {
			cursor.LastModified = m.LastModified
		}
	}
	return events, cursor, nil
}
