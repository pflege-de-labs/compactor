package crypto

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"filippo.io/age"
)

const ageScheme Scheme = "age"

// agePreamble is the fixed header every age ciphertext starts with,
// used as a Sniff fallback when a key lacks the .age extension.
const agePreamble = "age-encryption.org/v1"

type AgeCodec struct {
	recipients []age.Recipient
	identities []age.Identity
}

// NewAgeCodec parses recipients (required, used for Encrypt) and, if
// identityFile is non-empty, the matching identity (required for
// Decrypt — e.g. reading back an existing rollup to append to it, or
// materializing for DuckDB). A codec built without an identity can
// still encrypt; Decrypt returns an error until one is configured.
func NewAgeCodec(recipients []string, identityFile string) (*AgeCodec, error) {
	if len(recipients) == 0 {
		return nil, errors.New("crypto/age: at least one recipient is required")
	}

	parsedRecipients, err := age.ParseRecipients(strings.NewReader(strings.Join(recipients, "\n")))
	if err != nil {
		return nil, fmt.Errorf("crypto/age: parse recipients: %w", err)
	}

	c := &AgeCodec{recipients: parsedRecipients}

	if identityFile != "" {
		f, err := os.Open(identityFile)
		if err != nil {
			return nil, fmt.Errorf("crypto/age: open identity file: %w", err)
		}
		defer f.Close()

		identities, err := age.ParseIdentities(f)
		if err != nil {
			return nil, fmt.Errorf("crypto/age: parse identity file: %w", err)
		}
		c.identities = identities
	}

	return c, nil
}

func (c *AgeCodec) Scheme() Scheme { return ageScheme }
func (c *AgeCodec) KeyExt() string { return ".age" }

func (c *AgeCodec) Sniff(header []byte) bool {
	return bytes.HasPrefix(header, []byte(agePreamble))
}

func (c *AgeCodec) Encrypt(_ context.Context, w io.Writer, r io.Reader) error {
	wc, err := age.Encrypt(w, c.recipients...)
	if err != nil {
		return fmt.Errorf("crypto/age: encrypt: %w", err)
	}
	if _, err := io.Copy(wc, r); err != nil {
		_ = wc.Close()
		return fmt.Errorf("crypto/age: encrypt: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("crypto/age: encrypt: finalize: %w", err)
	}
	return nil
}

func (c *AgeCodec) Decrypt(_ context.Context, w io.Writer, r io.Reader, _ string) error {
	if len(c.identities) == 0 {
		return errors.New("crypto/age: no identity configured for decrypt")
	}
	rd, err := age.Decrypt(r, c.identities...)
	if err != nil {
		return fmt.Errorf("crypto/age: decrypt: %w", err)
	}
	if _, err := io.Copy(w, rd); err != nil {
		return fmt.Errorf("crypto/age: decrypt: %w", err)
	}
	return nil
}
