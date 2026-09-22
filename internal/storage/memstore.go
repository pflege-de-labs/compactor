package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemStore is an in-memory ObjectStore fake for tests. It implements the
// same conditional-write semantics as the S3 implementation.
type MemStore struct {
	mu      sync.Mutex
	objects map[string]memObject
	Now     func() time.Time
}

type memObject struct {
	body []byte
	meta ObjectMeta
}

func NewMemStore() *MemStore {
	return &MemStore{
		objects: make(map[string]memObject),
		Now:     time.Now,
	}
}

func etagFor(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func (m *MemStore) List(_ context.Context, prefix string) ([]ObjectMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []ObjectMeta
	for key, obj := range m.objects {
		if strings.HasPrefix(key, prefix) {
			out = append(out, obj.meta)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (m *MemStore) Get(_ context.Context, key string) (io.ReadCloser, ObjectMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	obj, ok := m.objects[key]
	if !ok {
		return nil, ObjectMeta{}, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(obj.body)), obj.meta, nil
}

func (m *MemStore) GetRange(_ context.Context, key string, n int64) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	obj, ok := m.objects[key]
	if !ok {
		return nil, ErrNotFound
	}
	if int64(len(obj.body)) <= n {
		return append([]byte(nil), obj.body...), nil
	}
	return append([]byte(nil), obj.body[:n]...), nil
}

func (m *MemStore) Put(_ context.Context, key string, body io.Reader, opts PutOptions) (ObjectMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, exists := m.objects[key]

	if opts.IfNoneMatch == "*" && exists {
		return ObjectMeta{}, ErrPreconditionFailed
	}
	if opts.IfMatchETag != "" {
		if !exists || existing.meta.ETag != opts.IfMatchETag {
			return ObjectMeta{}, ErrPreconditionFailed
		}
	}

	data, err := io.ReadAll(body)
	if err != nil {
		return ObjectMeta{}, err
	}

	meta := ObjectMeta{
		Key:          key,
		Size:         int64(len(data)),
		ETag:         etagFor(data),
		LastModified: m.Now(),
	}
	m.objects[key] = memObject{body: data, meta: meta}
	return meta, nil
}

func (m *MemStore) Head(_ context.Context, key string) (ObjectMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	obj, ok := m.objects[key]
	if !ok {
		return ObjectMeta{}, ErrNotFound
	}
	return obj.meta, nil
}
