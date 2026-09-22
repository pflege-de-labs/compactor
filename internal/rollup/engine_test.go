package rollup_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/pflege-de-labs/compactor/internal/checkpoint"
	"github.com/pflege-de-labs/compactor/internal/crypto"
	"github.com/pflege-de-labs/compactor/internal/rollup"
	"github.com/pflege-de-labs/compactor/internal/storage"
)

func newEngine(t *testing.T, registry *crypto.Registry) (*rollup.Engine, *storage.MemStore) {
	t.Helper()
	store := storage.NewMemStore()
	if registry == nil {
		registry = crypto.NewRegistry(crypto.NoopCodec{})
	}
	engine := &rollup.Engine{
		Store:        store,
		Lister:       storage.NewPollLister(store),
		Checkpoints:  checkpoint.NewS3Store(store, "checkpoints"),
		Registry:     registry,
		SourcePrefix: "source",
		RollupPrefix: "rollups",
		Clock:        time.Now,
	}
	return engine, store
}

func putSource(t *testing.T, store *storage.MemStore, day time.Time, name string, raw []byte) {
	t.Helper()
	key := "source/" + rollup.DayPrefix(day) + "/" + name
	if _, err := store.Put(context.Background(), key, bytes.NewReader(raw), storage.PutOptions{}); err != nil {
		t.Fatalf("seed source object %s: %v", key, err)
	}
}

func TestRunHourlyDayRollup_EmptyDayIsNoOp(t *testing.T) {
	engine, _ := newEngine(t, nil)
	day := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)

	result, err := engine.RunHourlyDayRollup(context.Background(), day)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.NoOp {
		t.Fatalf("expected NoOp result, got %+v", result)
	}
}

func TestRunHourlyDayRollup_IncrementalAppendAcrossTwoRuns(t *testing.T) {
	engine, store := newEngine(t, nil)
	ctx := context.Background()
	day := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)

	putSource(t, store, day, "a.json", []byte(`{"id":1}`))
	putSource(t, store, day, "b.json", []byte(`{"id":2}`))

	first, err := engine.RunHourlyDayRollup(ctx, day)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if first.ItemsAppended != 2 {
		t.Fatalf("first run: expected 2 items appended, got %d", first.ItemsAppended)
	}

	putSource(t, store, day, "c.json", []byte(`{"id":3}`))

	second, err := engine.RunHourlyDayRollup(ctx, day)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if second.ItemsAppended != 1 {
		t.Fatalf("second run: expected 1 new item appended, got %d", second.ItemsAppended)
	}

	body, _, err := store.Get(ctx, second.RollupKey)
	if err != nil {
		t.Fatalf("get rollup: %v", err)
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read rollup: %v", err)
	}
	lines := nonEmptyLines(data)
	if len(lines) != 3 {
		t.Fatalf("expected 3 JSONL lines, got %d: %q", len(lines), string(data))
	}
}

func TestRunHourlyDayRollup_EncryptedRoundTrip(t *testing.T) {
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

	engine, store := newEngine(t, registry)
	engine.OutputScheme = ageCodec.Scheme()
	ctx := context.Background()
	day := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)

	for i, raw := range [][]byte{[]byte(`{"id":1}`), []byte(`{"id":2}`)} {
		var ciphertext bytes.Buffer
		if err := ageCodec.Encrypt(ctx, &ciphertext, bytes.NewReader(raw)); err != nil {
			t.Fatalf("encrypt seed object: %v", err)
		}
		name := "event.json.age"
		if i == 1 {
			name = "event2.json.age"
		}
		putSource(t, store, day, name, ciphertext.Bytes())
	}

	result, err := engine.RunHourlyDayRollup(ctx, day)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.ItemsAppended != 2 {
		t.Fatalf("expected 2 items appended, got %d", result.ItemsAppended)
	}
	if filepath.Ext(result.RollupKey) != ".age" {
		t.Fatalf("expected encrypted rollup key, got %s", result.RollupKey)
	}

	body, _, err := store.Get(ctx, result.RollupKey)
	if err != nil {
		t.Fatalf("get rollup: %v", err)
	}
	defer body.Close()

	var plaintext bytes.Buffer
	if err := ageCodec.Decrypt(ctx, &plaintext, body, result.RollupKey); err != nil {
		t.Fatalf("decrypt rollup: %v", err)
	}
	lines := nonEmptyLines(plaintext.Bytes())
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSONL lines, got %d: %q", len(lines), plaintext.String())
	}
}

// Source objects are allowed to mix plaintext and encrypted (and, in
// principle, different encryption schemes) within one day — only the
// rollup's own output scheme is fixed (Engine.OutputScheme). Mixing no
// longer errors; it upgrades the day to encrypted output once any
// source event needs it.
func TestRunHourlyDayRollup_MixedSourceSchemesFoldIntoSingleOutputScheme(t *testing.T) {
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

	engine, store := newEngine(t, registry)
	engine.OutputScheme = ageCodec.Scheme()
	ctx := context.Background()
	day := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)

	putSource(t, store, day, "plain.json", []byte(`{"id":1}`))

	var ciphertext bytes.Buffer
	if err := ageCodec.Encrypt(ctx, &ciphertext, bytes.NewReader([]byte(`{"id":2}`))); err != nil {
		t.Fatalf("encrypt seed object: %v", err)
	}
	putSource(t, store, day, "encrypted.json.age", ciphertext.Bytes())

	result, err := engine.RunHourlyDayRollup(ctx, day)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.ItemsAppended != 2 {
		t.Fatalf("expected both plaintext and encrypted source objects folded in, got %d", result.ItemsAppended)
	}
	if filepath.Ext(result.RollupKey) != ".age" {
		t.Fatalf("expected the day to upgrade to encrypted output, got rollup key %s", result.RollupKey)
	}

	body, _, err := store.Get(ctx, result.RollupKey)
	if err != nil {
		t.Fatalf("get rollup: %v", err)
	}
	defer body.Close()
	var plaintext bytes.Buffer
	if err := ageCodec.Decrypt(ctx, &plaintext, body, result.RollupKey); err != nil {
		t.Fatalf("decrypt rollup: %v", err)
	}
	if lines := nonEmptyLines(plaintext.Bytes()); len(lines) != 2 {
		t.Fatalf("expected 2 JSONL lines, got %d: %q", len(lines), plaintext.String())
	}
}

// The real-world trigger for this: a source (e.g. a Redpanda
// Connect/Benthos pipeline) batches many events into one object as
// JSONL, not one event per object.
func TestRunHourlyDayRollup_JSONLBatchObjectSplitsIntoMultipleEvents(t *testing.T) {
	engine, store := newEngine(t, nil)
	ctx := context.Background()
	day := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)

	batch := []byte("{\"id\":1}\n{\"id\":2}\n{\"id\":3}\n")
	putSource(t, store, day, "batch.jsonl", batch)

	result, err := engine.RunHourlyDayRollup(ctx, day)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.ItemsAppended != 3 {
		t.Fatalf("expected 3 events split out of the one JSONL object, got %d", result.ItemsAppended)
	}

	body, _, err := store.Get(ctx, result.RollupKey)
	if err != nil {
		t.Fatalf("get rollup: %v", err)
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read rollup: %v", err)
	}
	if lines := nonEmptyLines(data); len(lines) != 3 {
		t.Fatalf("expected 3 JSONL lines, got %d: %q", len(lines), string(data))
	}
}

// Reproduces the keybaerchive pipeline's source format end-to-end: a
// gzip-compressed, AES-CTR-encrypted JSONL batch, IV embedded in the
// filename. compactor decodes it but re-encrypts the rollup with its
// own configured output scheme (age here), never with aes-ctr-gzip —
// that codec is source-decode-only.
func TestRunHourlyDayRollup_AESCTRGzipSourceDecodedAndReencryptedWithOutputScheme(t *testing.T) {
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

	aesKey := bytes.Repeat([]byte{0x24}, 32)
	aesCodec, err := crypto.NewAESCTRGzipCodec(hex.EncodeToString(aesKey))
	if err != nil {
		t.Fatalf("new aes-ctr-gzip codec: %v", err)
	}

	registry := crypto.NewRegistry(crypto.NoopCodec{}, ageCodec, aesCodec)
	engine, store := newEngine(t, registry)
	engine.OutputScheme = ageCodec.Scheme()
	ctx := context.Background()
	day := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)

	batch := []byte("{\"id\":1}\n{\"id\":2}\n")
	ciphertext, filename := encodeLikeKeybaerchiveForTest(t, aesKey, batch)
	putSource(t, store, day, filename, ciphertext)

	result, err := engine.RunHourlyDayRollup(ctx, day)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.ItemsAppended != 2 {
		t.Fatalf("expected 2 events decoded from the batch, got %d", result.ItemsAppended)
	}
	if filepath.Ext(result.RollupKey) != ".age" {
		t.Fatalf("expected rollup output to use the configured age output scheme, got %s", result.RollupKey)
	}
}

func TestRunPromoteWeek_ShortCircuitsWhenUnchanged(t *testing.T) {
	engine, store := newEngine(t, nil)
	ctx := context.Background()

	monday := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC) // 2026-W39 Monday
	isoYear, isoWeek := monday.ISOWeek()

	for i := range 7 {
		day := monday.AddDate(0, 0, i)
		putSource(t, store, day, "e.json", []byte(`{"id":1}`))
		if _, err := engine.RunHourlyDayRollup(ctx, day); err != nil {
			t.Fatalf("day rollup %d: %v", i, err)
		}
	}

	first, err := engine.RunPromoteWeek(ctx, isoYear, isoWeek)
	if err != nil {
		t.Fatalf("first promote: %v", err)
	}
	if first.NoOp {
		t.Fatalf("expected first promote to write a rollup, got NoOp")
	}

	second, err := engine.RunPromoteWeek(ctx, isoYear, isoWeek)
	if err != nil {
		t.Fatalf("second promote: %v", err)
	}
	if !second.NoOp {
		t.Fatalf("expected second promote to short-circuit as NoOp, got %+v", second)
	}
}

// encodeLikeKeybaerchiveForTest replicates the upstream pipeline's
// write path (gzip -> AES-CTR, IV embedded in the filename) so tests
// can seed a realistic source object without needing Encrypt support
// on AESCTRGzipCodec (which is decode-only).
func encodeLikeKeybaerchiveForTest(t *testing.T, key, plaintext []byte) (ciphertext []byte, filename string) {
	t.Helper()

	var gzBuf bytes.Buffer
	gz := gzip.NewWriter(&gzBuf)
	if _, err := gz.Write(plaintext); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		t.Fatalf("generate iv: %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	stream := cipher.NewCTR(block, iv)
	ct := make([]byte, gzBuf.Len())
	stream.XORKeyStream(ct, gzBuf.Bytes())

	return ct, "1765891212-" + hex.EncodeToString(iv) + ".jsonl.gz.enc"
}

func nonEmptyLines(data []byte) [][]byte {
	var lines [][]byte
	for line := range bytes.SplitSeq(data, []byte("\n")) {
		if len(line) > 0 {
			lines = append(lines, line)
		}
	}
	return lines
}
