// Package crypto abstracts client-side encryption of rollup content
// behind a Codec interface so the rollup engine never hardcodes a
// specific encryption scheme. Every codec speaks whole-buffer
// encrypt/decrypt: AEAD ciphers need a fresh nonce per encryption, so
// rollups are always rewritten whole rather than appended to in place.
package crypto

import (
	"context"
	"fmt"
	"io"
)

type Scheme string

const SchemeNone Scheme = "none"

type Encryptor interface {
	Scheme() Scheme
	Encrypt(ctx context.Context, w io.Writer, r io.Reader) error
	// KeyExt is the file-extension suffix (e.g. ".age") this codec's
	// output is tagged with, used by Detect to identify scheme on read.
	KeyExt() string
}

type Decryptor interface {
	Scheme() Scheme
	// Decrypt reverses Encrypt. key is the source object's key; most
	// codecs ignore it, but some ciphers used by upstream producers
	// embed per-object key material (e.g. an IV) in the filename rather
	// than the object body, and need it to decrypt at all.
	Decrypt(ctx context.Context, w io.Writer, r io.Reader, key string) error
}

// Sniffer is implemented by codecs whose ciphertext has a recognizable
// header, used as a fallback when the key extension is missing or
// ambiguous.
type Sniffer interface {
	Sniff(header []byte) bool
}

type Codec interface {
	Encryptor
	Decryptor
}

// Registry looks up codecs by scheme and detects scheme from an object
// key and/or its content header.
type Registry struct {
	codecs map[Scheme]Codec
}

func NewRegistry(codecs ...Codec) *Registry {
	r := &Registry{codecs: make(map[Scheme]Codec, len(codecs))}
	for _, c := range codecs {
		r.codecs[c.Scheme()] = c
	}
	return r
}

func (r *Registry) For(s Scheme) (Codec, bool) {
	c, ok := r.codecs[s]
	return c, ok
}

func (r *Registry) MustFor(s Scheme) (Codec, error) {
	c, ok := r.For(s)
	if !ok {
		return nil, fmt.Errorf("crypto: no codec registered for scheme %q", s)
	}
	return c, nil
}

// Detect identifies the scheme of an object from its key's extension
// first, falling back to sniffing the content header. Returns
// SchemeNone if nothing matches.
func (r *Registry) Detect(key string, header []byte) Scheme {
	for scheme, c := range r.codecs {
		if scheme == SchemeNone {
			continue
		}
		if ext := c.KeyExt(); ext != "" && hasSuffix(key, ext) {
			return scheme
		}
	}
	for scheme, c := range r.codecs {
		if scheme == SchemeNone {
			continue
		}
		if sniffer, ok := c.(Sniffer); ok && sniffer.Sniff(header) {
			return scheme
		}
	}
	return SchemeNone
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}
