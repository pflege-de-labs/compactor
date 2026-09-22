package crypto

import (
	"context"
	"io"
)

// NoopCodec passes data through unchanged, for rollup units whose
// source events are not encrypted.
type NoopCodec struct{}

func (NoopCodec) Scheme() Scheme { return SchemeNone }
func (NoopCodec) KeyExt() string { return "" }

func (NoopCodec) Encrypt(_ context.Context, w io.Writer, r io.Reader) error {
	_, err := io.Copy(w, r)
	return err
}

func (NoopCodec) Decrypt(_ context.Context, w io.Writer, r io.Reader, _ string) error {
	_, err := io.Copy(w, r)
	return err
}
