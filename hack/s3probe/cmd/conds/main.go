// conds probes multipart-upload semantics compactor would rely on.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	. "probe"
)

const MiB = 1 << 20

var c = Client(os.Getenv("LOGREQ") == "2")

func code(err error) string {
	if err == nil {
		return "OK"
	}
	var ae smithy.APIError
	if errors.As(err, &ae) {
		return ae.ErrorCode()
	}
	return err.Error()
}

func put(key string, data []byte) string {
	key = Prefix + key
	out, err := c.PutObject(Ctx, &s3.PutObjectInput{Bucket: &Bucket, Key: &key, Body: bytes.NewReader(data)})
	Must(err)
	return *out.ETag
}

func get(key string) []byte {
	key = Prefix + key
	out, err := c.GetObject(Ctx, &s3.GetObjectInput{Bucket: &Bucket, Key: &key})
	Must(err)
	defer out.Body.Close()
	b, _ := io.ReadAll(out.Body)
	return b
}

type part struct {
	data   []byte // nil => copy
	src    string
	srcIfM string
}

// mpu runs Create/UploadPart(Copy)/Complete with the given Complete conditions.
func mpu(key string, parts []part, ifNoneMatch, ifMatch string) (string, error) {
	key = Prefix + key
	cr, err := c.CreateMultipartUpload(Ctx, &s3.CreateMultipartUploadInput{Bucket: &Bucket, Key: &key})
	Must(err)
	var done []types.CompletedPart
	for i, p := range parts {
		n := aws.Int32(int32(i + 1))
		if p.data != nil {
			o, err := c.UploadPart(Ctx, &s3.UploadPartInput{Bucket: &Bucket, Key: &key, UploadId: cr.UploadId, PartNumber: n, Body: bytes.NewReader(p.data)})
			Must(err)
			done = append(done, types.CompletedPart{ETag: o.ETag, PartNumber: n})
			continue
		}
		in := &s3.UploadPartCopyInput{Bucket: &Bucket, Key: &key, UploadId: cr.UploadId, PartNumber: n, CopySource: aws.String(Bucket + "/" + Prefix + p.src)}
		if p.srcIfM != "" {
			in.CopySourceIfMatch = &p.srcIfM
		}
		o, err := c.UploadPartCopy(Ctx, in)
		if err != nil {
			c.AbortMultipartUpload(Ctx, &s3.AbortMultipartUploadInput{Bucket: &Bucket, Key: &key, UploadId: cr.UploadId})
			return "", fmt.Errorf("uploadpartcopy: %w", err)
		}
		done = append(done, types.CompletedPart{ETag: o.CopyPartResult.ETag, PartNumber: n})
	}
	in := &s3.CompleteMultipartUploadInput{Bucket: &Bucket, Key: &key, UploadId: cr.UploadId, MultipartUpload: &types.CompletedMultipartUpload{Parts: done}}
	if ifNoneMatch != "" {
		in.IfNoneMatch = &ifNoneMatch
	}
	if ifMatch != "" {
		in.IfMatch = &ifMatch
	}
	out, err := c.CompleteMultipartUpload(Ctx, in)
	if err != nil {
		// Leave it to the abort/list check below.
		return "", err
	}
	return *out.ETag, nil
}

func main() {
	_, err := c.CreateBucket(Ctx, &s3.CreateBucketInput{Bucket: &Bucket})
	fmt.Println("create bucket:", code(err))

	six := bytes.Repeat([]byte("{\"x\":1}\n"), 6*MiB/8)
	small := []byte("{\"tail\":true}\n")

	base := put("k", []byte("{\"old\":1}\n"))
	fmt.Println("base etag:", base)

	_, err = mpu("k", []part{{data: six}, {data: small}}, "*", "")
	fmt.Println("1 complete If-None-Match:* on existing key:", code(err), "| object unchanged:", string(get("k")) == "{\"old\":1}\n")

	_, err = mpu("k", []part{{data: six}, {data: small}}, "", `"deadbeefdeadbeefdeadbeefdeadbeef"`)
	fmt.Println("2 complete If-Match wrong etag:", code(err), "| object unchanged:", string(get("k")) == "{\"old\":1}\n")

	et, err := mpu("k", []part{{data: six}, {data: small}}, "", base)
	fmt.Println("3 complete If-Match right etag:", code(err), "| new etag:", et, "| multipart etag has -N suffix:", strings.Contains(et, "-"))

	et2, err := mpu("fresh", []part{{data: six}, {data: small}}, "*", "")
	fmt.Println("4 complete If-None-Match:* on new key:", code(err), et2)

	// 5: manager.Uploader forwards PutObjectInput.IfNoneMatch to Complete?
	lc := Client(os.Getenv("LOGREQ") == "1")
	up := manager.NewUploader(lc, func(u *manager.Uploader) { u.PartSize = 5 * MiB; u.Concurrency = 1 })
	_, err = up.Upload(Ctx, &s3.PutObjectInput{Bucket: &Bucket, Key: aws.String(Prefix + "fresh"), Body: io.MultiReader(bytes.NewReader(six), bytes.NewReader(small)), IfNoneMatch: aws.String("*")})
	fmt.Println("5 manager.Uploader IfNoneMatch on existing key:", code(err))

	// 6: UploadPartCopy semantics (server-side append for plain JSONL).
	smallSrcETag := put("small-src", []byte("{\"s\":1}\n"))
	_, err = mpu("cat1", []part{{src: "small-src"}, {data: small}}, "*", "")
	fmt.Println("6a copy <5MiB source as non-last part:", code(err))
	bigSrc := put("big-src", six)
	_, err = mpu("cat2", []part{{src: "big-src", srcIfM: `"0123456789abcdef0123456789abcdef"`}, {data: small}}, "*", "")
	fmt.Println("6b UploadPartCopy CopySourceIfMatch wrong:", code(err))
	_, err = mpu("cat3", []part{{src: "big-src", srcIfM: bigSrc}, {data: small}}, "*", "")
	got := get("cat3")
	fmt.Println("6c copy 6MiB + upload tail:", code(err), "| content == src+tail:", bytes.Equal(got, append(append([]byte{}, six...), small...)))
	_ = smallSrcETag

	// 7: leftovers are visible via ListMultipartUploads and can be aborted.
	lm, err := c.ListMultipartUploads(Ctx, &s3.ListMultipartUploadsInput{Bucket: &Bucket, Prefix: aws.String(Prefix)})
	Must(err)
	fmt.Println("7 incomplete uploads after failures:", len(lm.Uploads))
	for _, u := range lm.Uploads {
		_, err := c.AbortMultipartUpload(Ctx, &s3.AbortMultipartUploadInput{Bucket: &Bucket, Key: u.Key, UploadId: u.UploadId})
		Must(err)
	}
	lm, _ = c.ListMultipartUploads(Ctx, &s3.ListMultipartUploadsInput{Bucket: &Bucket, Prefix: aws.String(Prefix)})
	fmt.Println("7 incomplete uploads after abort:", len(lm.Uploads))
}
