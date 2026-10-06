package crypto_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"io"
	"testing"

	"github.com/pflege-de-labs/compactor/internal/crypto"
)

// encodeLikeKeybaerchive replicates the upstream Redpanda Connect
// pipeline's write path (archive(lines) -> gzip -> AES-CTR) so the test
// can verify AESCTRGzipCodec.Decrypt against a realistic ciphertext
// without relying on Encrypt, which this codec doesn't support.
func encodeLikeKeybaerchive(t *testing.T, key, plaintext []byte) (ciphertext []byte, keyName string) {
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

	name := "1765891212-" + hex.EncodeToString(iv) + ".jsonl.gz.enc"
	return ct, name
}

func TestAESCTRGzipCodec_DecryptsKeybaerchiveFormat(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32) // AES-256
	want := []byte("{\"id\":1}\n{\"id\":2}\n{\"id\":3}\n")

	ciphertext, keyName := encodeLikeKeybaerchive(t, key, want)

	codec, err := crypto.NewAESCTRGzipCodec(hex.EncodeToString(key))
	if err != nil {
		t.Fatalf("new codec: %v", err)
	}

	var got bytes.Buffer
	if err := codec.Decrypt(context.Background(), &got, bytes.NewReader(ciphertext), keyName); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("decrypt mismatch: got %q, want %q", got.Bytes(), want)
	}
}

func TestAESCTRGzipCodec_KeyWithoutIVFails(t *testing.T) {
	codec, err := crypto.NewAESCTRGzipCodec(hex.EncodeToString(bytes.Repeat([]byte{0x01}, 16)))
	if err != nil {
		t.Fatalf("new codec: %v", err)
	}
	if err := codec.Decrypt(context.Background(), &bytes.Buffer{}, bytes.NewReader(nil), "no-iv-here.jsonl.gz.enc"); err == nil {
		t.Fatal("expected an error for a key with no embedded IV")
	}
}

func TestAESCTRGzipCodec_EncryptIsUnsupported(t *testing.T) {
	codec, err := crypto.NewAESCTRGzipCodec(hex.EncodeToString(bytes.Repeat([]byte{0x01}, 16)))
	if err != nil {
		t.Fatalf("new codec: %v", err)
	}
	if err := codec.Encrypt(context.Background(), &bytes.Buffer{}, bytes.NewReader(nil)); err == nil {
		t.Fatal("expected Encrypt to be unsupported")
	}
}

func TestNewAESCTRGzipCodec_RejectsBadKeyLength(t *testing.T) {
	if _, err := crypto.NewAESCTRGzipCodec(hex.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("expected an error for a key of invalid length")
	}
}

func TestAESCTRGzipCodec_RejectsDecompressionBomb(t *testing.T) {
	if testing.Short() {
		t.Skip("inflates 256 MiB of zeros")
	}

	key := bytes.Repeat([]byte{0x42}, 32)
	chunk := make([]byte, 1<<20)
	var plaintext bytes.Buffer
	for range 257 {
		plaintext.Write(chunk)
	}
	ciphertext, keyName := encodeLikeKeybaerchive(t, key, plaintext.Bytes())

	codec, err := crypto.NewAESCTRGzipCodec(hex.EncodeToString(key))
	if err != nil {
		t.Fatalf("new codec: %v", err)
	}
	if err := codec.Decrypt(context.Background(), io.Discard, bytes.NewReader(ciphertext), keyName); err == nil {
		t.Fatal("expected an error for an object that decompresses past the size cap")
	}
}
