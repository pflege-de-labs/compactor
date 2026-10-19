package rollup

import (
	"bytes"
	"io"
	"math/rand/v2"
	"testing"
)

func TestChunkBuffer_RoundTripAcrossChunkBoundaries(t *testing.T) {
	var want bytes.Buffer
	var b chunkBuffer
	// Odd write sizes straddle chunk boundaries, including writes larger than a chunk.
	for _, n := range []int{1, chunkSize - 1, 2, 3*chunkSize + 7, 0, 12345} {
		p := make([]byte, n)
		for i := range p {
			p[i] = byte(rand.IntN(256))
		}
		want.Write(p)
		if _, err := b.Write(p); err != nil {
			t.Fatal(err)
		}
	}
	if b.Len() != int64(want.Len()) {
		t.Fatalf("Len = %d, want %d", b.Len(), want.Len())
	}

	got, err := io.ReadAll(b.Reader())
	if err != nil || !bytes.Equal(got, want.Bytes()) {
		t.Fatalf("Reader returned %d bytes (err %v), want %d identical bytes", len(got), err, want.Len())
	}

	// A seek into the middle of a chunk, as an upload retry does.
	r := b.Reader()
	off := int64(chunkSize + 5)
	if _, err := r.Seek(off, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	rest, _ := io.ReadAll(r)
	if !bytes.Equal(rest, want.Bytes()[off:]) {
		t.Fatal("read after seek differs")
	}
}
