package probe

import (
	"context"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go/logging"
)

// Prefix keeps every probe object and multipart upload apart from real data in the bucket.
const Prefix = "s3probe/"

// Client mirrors compactor's S3Store client options (path style, checksums only when required).
// S3_ENDPOINT, AWS_REGION, AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY select the target; the defaults fit a local SILO.
func Client(logReq bool) *s3.Client {
	cfg := aws.Config{
		Region: env("AWS_REGION", "us-east-1"), Logger: logging.NewStandardLogger(os.Stderr),
		Credentials: credentials.NewStaticCredentialsProvider(env("AWS_ACCESS_KEY_ID", "probe"), env("AWS_SECRET_ACCESS_KEY", "probeprobe"), ""),
	}
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(env("S3_ENDPOINT", "http://127.0.0.1:19000"))
		o.UsePathStyle = true
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
		if logReq {
			o.ClientLogMode = aws.LogRequest
		}
	})
}

func Must(err error) {
	if err != nil {
		panic(err)
	}
}

var Ctx = context.Background()
var Bucket = func() string {
	b := os.Getenv("BUCKET")
	if b == "" {
		panic("BUCKET is required")
	}
	return b
}()

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
