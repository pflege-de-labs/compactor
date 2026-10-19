// stream measures peak memory of an age-encrypted day rollup streamed through a
// hand-rolled multipart writer, including the incremental "read existing, append, rewrite" path.
package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"

	"filippo.io/age"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	. "probe"
)

const MiB = 1 << 20

var c = Client(false)

// mpWriter buffers one part at a time; small objects fall back to a single PUT.
type mpWriter struct {
	key, uploadID string
	buf           []byte
	parts         []types.CompletedPart
	ifNoneMatch   string
	ifMatchHead   string // emulated If-Match: HEAD right before Complete (ADR 0012)
	size          int64
}

func (w *mpWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		k := min(len(p), cap(w.buf)-len(w.buf))
		w.buf = append(w.buf, p[:k]...)
		p = p[k:]
		if len(w.buf) == cap(w.buf) {
			if err := w.flush(); err != nil {
				return 0, err
			}
		}
	}
	w.size += int64(n)
	return n, nil
}

func (w *mpWriter) flush() error {
	if w.uploadID == "" {
		cr, err := c.CreateMultipartUpload(Ctx, &s3.CreateMultipartUploadInput{Bucket: &Bucket, Key: &w.key})
		if err != nil {
			return err
		}
		w.uploadID = *cr.UploadId
	}
	n := aws.Int32(int32(len(w.parts) + 1))
	o, err := c.UploadPart(Ctx, &s3.UploadPartInput{Bucket: &Bucket, Key: &w.key, UploadId: &w.uploadID, PartNumber: n, Body: bytes.NewReader(w.buf)})
	if err != nil {
		return err
	}
	w.parts = append(w.parts, types.CompletedPart{ETag: o.ETag, PartNumber: n})
	w.buf = w.buf[:0]
	return nil
}

func (w *mpWriter) Close() (string, error) {
	if w.uploadID == "" { // fits one part: plain PUT, same as today
		in := &s3.PutObjectInput{Bucket: &Bucket, Key: &w.key, Body: bytes.NewReader(w.buf)}
		if w.ifNoneMatch != "" {
			in.IfNoneMatch = &w.ifNoneMatch
		}
		o, err := c.PutObject(Ctx, in)
		if err != nil {
			return "", err
		}
		return *o.ETag, nil
	}
	if len(w.buf) > 0 {
		if err := w.flush(); err != nil {
			return "", err
		}
	}
	if w.ifMatchHead != "" {
		h, err := c.HeadObject(Ctx, &s3.HeadObjectInput{Bucket: &Bucket, Key: &w.key})
		if err != nil || *h.ETag != w.ifMatchHead {
			return "", errors.New("precondition failed (HEAD)")
		}
	}
	in := &s3.CompleteMultipartUploadInput{Bucket: &Bucket, Key: &w.key, UploadId: &w.uploadID, MultipartUpload: &types.CompletedMultipartUpload{Parts: w.parts}}
	if w.ifNoneMatch != "" {
		in.IfNoneMatch = &w.ifNoneMatch
	}
	o, err := c.CompleteMultipartUpload(Ctx, in)
	if err != nil {
		return "", err
	}
	return *o.ETag, nil
}

func (w *mpWriter) Abort() {
	if w.uploadID != "" {
		c.AbortMultipartUpload(Ctx, &s3.AbortMultipartUploadInput{Bucket: &Bucket, Key: &w.key, UploadId: &w.uploadID})
	}
}

// writeEvents writes about n bytes of keycloak-like JSONL in ~700 KB batches, like the real sources.
func writeEvents(w io.Writer, n int64, tag string) (int64, error) {
	var lines int64
	var written int64
	batch := make([]byte, 0, 800*1024)
	for written < n {
		batch = batch[:0]
		for i := 0; i < 999; i++ {
			batch = fmt.Appendf(batch, `{"time":%d,"type":"LOGIN","realmId":"r","clientId":"c","userId":"u-%d","ipAddress":"10.0.0.1","details":{"tag":%q,"pad":"%0400d"}}`+"\n", lines, lines, tag, lines)
			lines++
		}
		if _, err := w.Write(batch); err != nil {
			return lines, err
		}
		written += int64(len(batch))
	}
	return lines, nil
}

var peakHeap atomic.Uint64

func sample(stop chan struct{}) {
	var ms runtime.MemStats
	for {
		select {
		case <-stop:
			return
		case <-time.After(20 * time.Millisecond):
			runtime.ReadMemStats(&ms)
			if ms.HeapInuse > peakHeap.Load() {
				peakHeap.Store(ms.HeapInuse)
			}
		}
	}
}

func main() {
	mode := flag.String("mode", "seed", "seed|append|count")
	sizeMiB := flag.Int64("mib", 1024, "plaintext MiB to write")
	partMiB := flag.Int("part", 16, "part size MiB")
	flag.Parse()

	raw, err := os.ReadFile("id.txt") // seed/append/count share one identity
	if err != nil {
		g, _ := age.GenerateX25519Identity()
		raw = []byte(g.String())
		Must(os.WriteFile("id.txt", raw, 0o600))
	}
	id, err := age.ParseX25519Identity(string(raw))
	Must(err)
	key := Prefix + "rollups/day/2026/01/23.jsonl.age"

	stop := make(chan struct{})
	go sample(stop)
	start := time.Now()

	c.CreateBucket(Ctx, &s3.CreateBucketInput{Bucket: &Bucket})

	switch *mode {
	case "seed", "append":
		w := &mpWriter{key: key, buf: make([]byte, 0, *partMiB*MiB)}
		var existing io.Reader
		if *mode == "seed" {
			w.ifNoneMatch = "*"
		} else {
			g, err := c.GetObject(Ctx, &s3.GetObjectInput{Bucket: &Bucket, Key: &key})
			Must(err)
			defer g.Body.Close()
			w.ifMatchHead = *g.ETag
			existing, err = age.Decrypt(g.Body, id)
			Must(err)
		}
		pr, pw := io.Pipe()
		var lines int64
		go func() { // producer: existing rollup first, then new events
			if existing != nil {
				if _, err := io.Copy(pw, existing); err != nil {
					pw.CloseWithError(err)
					return
				}
			}
			n, err := writeEvents(pw, *sizeMiB*MiB, *mode)
			lines = n
			pw.CloseWithError(err)
		}()
		enc, err := age.Encrypt(w, id.Recipient())
		Must(err)
		if _, err := io.Copy(enc, pr); err != nil {
			w.Abort()
			panic(err)
		}
		Must(enc.Close())
		etag, err := w.Close()
		if err != nil {
			w.Abort()
			panic(err)
		}
		fmt.Printf("%s: wrote %d MiB ciphertext in %d parts, new lines %d, etag %s\n", *mode, w.size/MiB, len(w.parts), lines, etag)
	case "count":
		g, err := c.GetObject(Ctx, &s3.GetObjectInput{Bucket: &Bucket, Key: &key})
		Must(err)
		r, err := age.Decrypt(g.Body, id)
		Must(err)
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		counts := map[bool]int{}
		for sc.Scan() {
			counts[bytes.Contains(sc.Bytes(), []byte(`"tag":"append"`))]++
		}
		Must(sc.Err())
		fmt.Printf("count: seed lines %d, appended lines %d\n", counts[false], counts[true])
	}
	close(stop)
	var ru syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	fmt.Printf("  elapsed %s, peak HeapInuse %.1f MiB, max RSS %.1f MiB\n", time.Since(start).Round(time.Second), float64(peakHeap.Load())/MiB, float64(ru.Maxrss)/MiB)
}
