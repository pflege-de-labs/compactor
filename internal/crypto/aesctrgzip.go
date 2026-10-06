package crypto

import (
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

const aesCTRGzipScheme Scheme = "aes-ctr-gzip"

// AES-CTR has no integrity tag, so a corrupt object could inflate without bound into memory; real batches are far smaller.
const maxDecompressedSize = 256 << 20

// AESCTRGzipCodec reads a source format some upstream producers use
// (e.g. a Redpanda Connect / Benthos pipeline batching events into
// gzip-compressed, AES-CTR-encrypted JSONL files): the object body is
// AES-CTR ciphertext wrapping gzip-compressed JSONL, and the 16-byte
// IV is embedded in the object key's filename rather than the body
// (there is no AEAD tag — AES-CTR provides no integrity check).
//
// This codec is source-decode-only: compactor never writes this
// format, only reads it. Encrypt returns an error.
type AESCTRGzipCodec struct {
	key []byte
}

// NewAESCTRGzipCodec parses a hex-encoded AES key (16/24/32 bytes
// after decoding, for AES-128/192/256).
func NewAESCTRGzipCodec(hexKey string) (*AESCTRGzipCodec, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("crypto/aesctrgzip: decode key: %w", err)
	}
	switch len(key) {
	case 16, 24, 32:
	default:
		return nil, fmt.Errorf("crypto/aesctrgzip: key must decode to 16, 24, or 32 bytes, got %d", len(key))
	}
	return &AESCTRGzipCodec{key: key}, nil
}

func (c *AESCTRGzipCodec) Scheme() Scheme { return aesCTRGzipScheme }
func (c *AESCTRGzipCodec) KeyExt() string { return ".jsonl.gz.enc" }

func (c *AESCTRGzipCodec) Encrypt(_ context.Context, _ io.Writer, _ io.Reader) error {
	return errors.New("crypto/aesctrgzip: encrypt is not supported, this codec only decodes an upstream source format")
}

// Decrypt AES-CTR-decrypts the body using the IV embedded in key's
// filename, then gunzips the result — the reverse of the producer's
// archive(lines) -> compress(gzip) -> encrypt_aes(ctr) pipeline. The
// output is JSONL, ready for the caller to split into events.
func (c *AESCTRGzipCodec) Decrypt(_ context.Context, w io.Writer, r io.Reader, key string) error {
	iv, err := ivFromKey(key)
	if err != nil {
		return fmt.Errorf("crypto/aesctrgzip: %w", err)
	}

	block, err := aes.NewCipher(c.key)
	if err != nil {
		return fmt.Errorf("crypto/aesctrgzip: new cipher: %w", err)
	}
	stream := cipher.NewCTR(block, iv)

	gz, err := gzip.NewReader(&cipher.StreamReader{S: stream, R: r})
	if err != nil {
		return fmt.Errorf("crypto/aesctrgzip: gzip reader: %w", err)
	}
	defer gz.Close()

	n, err := io.Copy(w, io.LimitReader(gz, maxDecompressedSize+1))
	if err != nil {
		return fmt.Errorf("crypto/aesctrgzip: decompress: %w", err)
	}
	if n > maxDecompressedSize {
		return fmt.Errorf("crypto/aesctrgzip: %q decompresses to more than %d bytes", key, maxDecompressedSize)
	}
	return nil
}

// ivFromKey extracts the 16-byte IV the producer embeds in the object
// key's filename: "<...>-<32 hex chars>.jsonl.gz.enc".
func ivFromKey(key string) ([]byte, error) {
	base := strings.TrimSuffix(path.Base(key), ".jsonl.gz.enc")
	idx := strings.LastIndex(base, "-")
	if idx < 0 {
		return nil, fmt.Errorf("key %q has no \"-<iv>\" suffix", key)
	}

	iv, err := hex.DecodeString(base[idx+1:])
	if err != nil {
		return nil, fmt.Errorf("key %q: decode iv: %w", key, err)
	}
	if len(iv) != aes.BlockSize {
		return nil, fmt.Errorf("key %q: iv must be %d bytes, got %d", key, aes.BlockSize, len(iv))
	}
	return iv, nil
}
