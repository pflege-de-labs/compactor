package crypto_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"

	"github.com/pflege-de-labs/compactor/internal/crypto"
)

func newTestAgeCodec(t *testing.T) *crypto.AgeCodec {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	identityFile := filepath.Join(t.TempDir(), "identity.txt")
	if err := os.WriteFile(identityFile, []byte(identity.String()+"\n"), 0o600); err != nil {
		t.Fatalf("write identity file: %v", err)
	}
	codec, err := crypto.NewAgeCodec([]string{identity.Recipient().String()}, identityFile)
	if err != nil {
		t.Fatalf("new age codec: %v", err)
	}
	return codec
}

func TestAgeCodec_RoundTrip(t *testing.T) {
	codec := newTestAgeCodec(t)
	ctx := context.Background()
	want := []byte(`{"id":1,"event":"login"}`)

	var ciphertext bytes.Buffer
	if err := codec.Encrypt(ctx, &ciphertext, bytes.NewReader(want)); err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if bytes.Equal(ciphertext.Bytes(), want) {
		t.Fatalf("ciphertext should not equal plaintext")
	}

	var plaintext bytes.Buffer
	if err := codec.Decrypt(ctx, &plaintext, bytes.NewReader(ciphertext.Bytes()), "irrelevant-key"); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(plaintext.Bytes(), want) {
		t.Fatalf("round-trip mismatch: got %q, want %q", plaintext.Bytes(), want)
	}
}

func TestAgeCodec_DecryptWithoutIdentityFails(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	codec, err := crypto.NewAgeCodec([]string{identity.Recipient().String()}, "")
	if err != nil {
		t.Fatalf("new age codec: %v", err)
	}

	if err := codec.Decrypt(context.Background(), &bytes.Buffer{}, bytes.NewReader(nil), "irrelevant-key"); err == nil {
		t.Fatalf("expected error decrypting without a configured identity")
	}
}

func TestRegistry_DetectByExtensionAndSniff(t *testing.T) {
	ageCodec := newTestAgeCodec(t)
	registry := crypto.NewRegistry(crypto.NoopCodec{}, ageCodec)

	if got := registry.Detect("rollups/day/2026/09/22.jsonl.age", nil); got != ageCodec.Scheme() {
		t.Fatalf("expected extension-based detection to find %q, got %q", ageCodec.Scheme(), got)
	}
	if got := registry.Detect("rollups/day/2026/09/22.jsonl", nil); got != crypto.SchemeNone {
		t.Fatalf("expected plain key to detect as %q, got %q", crypto.SchemeNone, got)
	}

	var ciphertext bytes.Buffer
	if err := ageCodec.Encrypt(context.Background(), &ciphertext, bytes.NewReader([]byte(`{}`))); err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if got := registry.Detect("no-extension-hint", ciphertext.Bytes()[:32]); got != ageCodec.Scheme() {
		t.Fatalf("expected header sniff to find %q, got %q", ageCodec.Scheme(), got)
	}
}
