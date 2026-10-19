# s3probe

Throwaway probes for Phase 0 of [streaming rollups](../../docs/proposals/streaming-rollups.md)
(issue #26): do multipart uploads, conditional completion and server-side part copy
work on a given S3 backend? A separate Go module, so it never builds into compactor.

Use a scratch bucket. Every object and multipart upload goes under `s3probe/`, and
only uploads under that prefix are aborted, but `conds` still tries `CreateBucket`.

```sh
export BUCKET=scratch S3_ENDPOINT=https://… AWS_REGION=… AWS_ACCESS_KEY_ID=… AWS_SECRET_ACCESS_KEY=…
go run ./cmd/conds                       # conditional Complete, UploadPartCopy, abort/list
go run ./cmd/stream -mode seed -mib 1024 # stream an age rollup in 16 MiB parts
go run ./cmd/stream -mode append -mib 64 # read it back, append, re-upload
go run ./cmd/stream -mode count          # verify line counts
mc rm --recursive --force <alias>/$BUCKET/s3probe/   # clean up
```

Without the variables it targets a local SILO on `127.0.0.1:19000` with credentials
`probe` / `probeprobe` (see `CONTRIBUTING.md`). `stream` writes a throwaway age
identity to `id.txt` in the working directory.

Results against local SILO (2026-10-19): conditional `CompleteMultipartUpload`
honors `If-None-Match`/`If-Match`; a copied part under 5 MiB fails with
`EntityTooSmall`; streaming a 64 MiB age rollup and appending to it peaks at
~34 MiB heap. SysEleven's Ceph RGW is untested.
