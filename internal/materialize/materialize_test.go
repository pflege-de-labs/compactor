package materialize_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/pflege-de-labs/compactor/internal/crypto"
	"github.com/pflege-de-labs/compactor/internal/materialize"
	"github.com/pflege-de-labs/compactor/internal/storage"
)

func TestMaterialize_DecryptsEncryptedRollup(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	identityFile := filepath.Join(t.TempDir(), "identity.txt")
	if err := os.WriteFile(identityFile, []byte(identity.String()+"\n"), 0o600); err != nil {
		t.Fatalf("write identity file: %v", err)
	}
	ageCodec, err := crypto.NewAgeCodec([]string{identity.Recipient().String()}, identityFile)
	if err != nil {
		t.Fatalf("new age codec: %v", err)
	}
	registry := crypto.NewRegistry(crypto.NoopCodec{}, ageCodec)

	ctx := context.Background()
	want := []byte(`{"id":1}` + "\n" + `{"id":2}` + "\n")

	var ciphertext bytes.Buffer
	if err := ageCodec.Encrypt(ctx, &ciphertext, bytes.NewReader(want)); err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	store := storage.NewMemStore()
	key := "rollups/day/2026/09/22.jsonl.age"
	if _, err := store.Put(ctx, key, &ciphertext, storage.PutOptions{IfNoneMatch: "*"}); err != nil {
		t.Fatalf("seed rollup: %v", err)
	}

	outDir := t.TempDir()
	path, cleanup, err := materialize.Materialize(ctx, store, registry, key, outDir)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	defer cleanup()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read materialized file: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("materialized content mismatch: got %q, want %q", got, want)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected scratch file mode 0600, got %v", info.Mode().Perm())
	}
}

func TestSweepExpired_RemovesOnlyStaleFiles(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "stale")
	fresh := filepath.Join(dir, "fresh")

	if err := os.WriteFile(stale, []byte("x"), 0o600); err != nil {
		t.Fatalf("write stale: %v", err)
	}
	if err := os.WriteFile(fresh, []byte("x"), 0o600); err != nil {
		t.Fatalf("write fresh: %v", err)
	}
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stale, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if err := materialize.SweepExpired(dir, time.Hour); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("expected stale file to be removed, stat err=%v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("expected fresh file to survive, stat err=%v", err)
	}
}
