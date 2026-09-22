// Package materialize decrypts a rollup object into a local scratch
// file just before a DuckDB query touches it — DuckDB has no generic
// decryption support, so ciphertext must never be handed to it directly.
package materialize

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pflege-de-labs/compactor/internal/crypto"
	"github.com/pflege-de-labs/compactor/internal/storage"
)

// Materialize decrypts (if needed) the object at key into a local
// scratch file under outDir and returns its path plus a cleanup func
// that removes it. Scratch files are written 0600: decrypted data at
// rest here is a real, if brief, exposure window.
func Materialize(ctx context.Context, store storage.ObjectStore, registry *crypto.Registry, key, outDir string) (path string, cleanup func() error, err error) {
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return "", nil, fmt.Errorf("materialize: create scratch dir: %w", err)
	}

	header, err := store.GetRange(ctx, key, 64)
	if err != nil {
		return "", nil, fmt.Errorf("materialize: sniff %s: %w", key, err)
	}
	scheme := registry.Detect(key, header)
	codec, err := registry.MustFor(scheme)
	if err != nil {
		return "", nil, fmt.Errorf("materialize: %s: %w", key, err)
	}

	body, _, err := store.Get(ctx, key)
	if err != nil {
		return "", nil, fmt.Errorf("materialize: get %s: %w", key, err)
	}
	defer body.Close()

	outName := strings.ReplaceAll(strings.TrimPrefix(key, "/"), "/", "_")
	outName = strings.TrimSuffix(outName, codec.KeyExt())
	outPath := filepath.Join(outDir, outName)

	f, err := os.OpenFile(outPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", nil, fmt.Errorf("materialize: create scratch file: %w", err)
	}

	if err := codec.Decrypt(ctx, f, body, key); err != nil {
		f.Close()
		os.Remove(outPath)
		return "", nil, fmt.Errorf("materialize: decrypt %s: %w", key, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(outPath)
		return "", nil, fmt.Errorf("materialize: close scratch file: %w", err)
	}

	return outPath, func() error { return os.Remove(outPath) }, nil
}

// SweepExpired removes scratch files under dir older than ttl, so a
// leftover file from a crashed or --keep run doesn't accumulate
// plaintext indefinitely.
func SweepExpired(dir string, ttl time.Duration) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("materialize: read scratch dir: %w", err)
	}

	cutoff := time.Now().Add(-ttl)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
	return nil
}
