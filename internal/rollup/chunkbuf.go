package rollup

import "io"

const chunkSize = 1 << 20

// chunkBuffer is an append-only buffer of fixed-size blocks. Unlike a
// doubling bytes.Buffer, growing it never copies, so a rollup peaks at
// about 1x its size in memory instead of about 3x while it grows.
type chunkBuffer struct {
	chunks [][]byte // every chunk but the last is full, which ReadAt relies on
	size   int64
}

func (b *chunkBuffer) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		if len(b.chunks) == 0 || len(b.chunks[len(b.chunks)-1]) == chunkSize {
			b.chunks = append(b.chunks, make([]byte, 0, chunkSize))
		}
		last := &b.chunks[len(b.chunks)-1]
		k := min(len(p), chunkSize-len(*last))
		*last = append(*last, p[:k]...)
		p = p[k:]
	}
	b.size += int64(n)
	return n, nil
}

func (b *chunkBuffer) Len() int64 { return b.size }

func (b *chunkBuffer) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off >= b.size {
		return 0, io.EOF
	}
	i, o := int(off/chunkSize), int(off%chunkSize)
	n := 0
	for n < len(p) && i < len(b.chunks) {
		n += copy(p[n:], b.chunks[i][o:])
		i, o = i+1, 0
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// Reader returns a seekable view, which the S3 upload needs to sign and retry without copying.
func (b *chunkBuffer) Reader() *io.SectionReader { return io.NewSectionReader(b, 0, b.size) }
