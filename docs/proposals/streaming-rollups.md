# Proposal: streaming rollups with bounded memory

Status: proposal (research only, nothing implemented). Tracking issue: see
the "feat: streaming rollups with bounded memory" issue.

## Summary

Stream every rollup write through a hand-rolled S3 multipart writer: GET
and decrypt the existing day rollup or the children, append new events,
encrypt with age, and upload one part at a time. Memory then depends on
the part size, not on the rollup size. A local MinIO prototype held
about 35 MiB heap and 52 MiB RSS for both a 128 MiB and an 800 MiB age
rollup, including the incremental read-append-rewrite path. Streaming
needs no local storage. It is also required anyway: at January volume,
week and month rollups exceed the 5 GiB single-PUT limit.

Two caveats:

- Multipart behavior on SysEleven Ceph RGW is unverified. Run the probe
  in Phase 0 before building.
- Streaming fixes memory, not I/O. Each cycle still rewrites the whole
  day, week or month. Bounded part objects (option d) are the follow-up
  if that cost matters.

Separately, a pre-existing crash-safety bug in the day path (see
[Finding](#finding-crash-between-rollup-and-checkpoint-is-not-idempotent))
should be fixed first.

## Problem

| Unit (January keycloak volume) | Plaintext JSONL | Notes |
|---|---|---|
| Source batch | ~700 KB (999 events), ~37 KiB AES-CTR+gzip | ~10x compression |
| Day | ~1.1 GB | 115 MiB compressed sources |
| ISO week | ~7.7 GB | > 5 GiB single PUT |
| Month | ~30 GB; up to ~40 GB | `weeksInMonth` folds in whole ISO weeks, so 35–42 days |

Pods run with 1–2 GiB memory limits. PR #22 cut peak heap to about 1.5x
the rollup's JSONL size, but a day still needs more than 1.5 GiB, and a
week or month cannot fit at all. A single `PutObject` is capped at 5 GiB
on AWS; RGW's `rgw_max_put_size` defaults to the same, which is assumed
but not checked for SysEleven. So week and month need multipart (or
splitting) at any memory budget.

## Where memory is held today

Cited at PR #22 (`fix/memory-usage@3b09b8b`), with `origin/main` in brackets.

| Path | Location | What is held |
|---|---|---|
| Day: rollup plaintext | `internal/rollup/engine.go:165` [main `:164`, `bytes.Buffer`] | whole day plaintext (`chunkBuffer`) |
| Day: existing rollup | `engine.go:174-182` [main `:179`] | decrypted fully into that buffer |
| Day: per source object | `engine.go:208-218` [main `:207`] | one decoded batch (~700 KB, capped at 256 MiB by `aesctrgzip.go:19`) |
| Day: output | `engine.go:235`, `encode` `:271-280` [main `:234-235`] | age: a second full copy (ciphertext `chunkBuffer`, `:275`); none: no copy |
| Week | `promote.go:72`, `:82`, `decryptChildInto` `:191-206` [main `:73`, `:192`] | all 7 day plaintexts concatenated |
| Month | `promote.go:160`, `:170` [main `:161`] | all ISO-week plaintexts concatenated |
| Week/month output | `encryptAndPutRollup` `promote.go:211-212` [main `:213-214`] | age: second full copy |
| Upload | `internal/storage/s3.go:121-144` [main `:122` `io.ReadAll`, `:154`] | PR #22 sends the seekable buffer in place; still one `PutObject` |

Small fixed costs: checkpoint `ProcessedKeys` (~1.6k keys per day), age
64 KiB chunks. `materialize` already streams to its file
(`internal/materialize/materialize.go`).

## Constraints

- **ADR 0004:** the rollup is written before the checkpoint, and writes
  are conditional.
- **ADR 0012:** RGW rejects `If-Match` on PUT with 412 even for a
  matching ETag, while `If-None-Match: *` works. With
  `s3.if-match: false`, compactor HEADs, compares the ETag, then PUTs.
  This is single-writer only.
- **age (ADR 0003):** age is one STREAM per file. Concatenated age files
  do not decrypt. Verified with `cat a.age b.age | age -d`, which fails
  with "failed to decrypt and authenticate payload chunk". Ciphertext
  cannot be appended.
- **ADR 0008:** the output scheme is `encryption.scheme`, which can be
  `none` or `age`. The chart default is `none`. The production setting
  decides whether server-side composition (option c) applies at all.
- **ADR 0005, DuckDB:** DuckDB reads plain `.jsonl` and `.jsonl.gz`,
  including concatenated gzip members (verified, DuckDB v1.5.6). age
  output always needs `materialize`.

## Finding: crash between rollup and checkpoint is not idempotent

ADR 0004 assumes the retry "redoes the same merge". It does not. The
following was verified on `origin/main` with a throwaway test that fails
`SaveDay` once. PR #22 has the same logic (`engine.go:174-189`).

- **Update:** the retry reads the already-updated rollup, takes its
  ETag from the GET (`:188`), and appends the same "new" events again.
  The day was `{1}`, then `{2}` was added and the write crashed before
  the checkpoint. The retry produced `{1},{2},{2}`, a silent duplicate.
- **Create:** the checkpoint has no `RollupETag`, so the retry PUTs with
  `If-None-Match: *`. That gets 412 on every later run, and the day
  stays stuck until `checkpoint reset`.
- **Week/month create:** `encryptAndPutRollup` with an empty `priorETag`
  has the same shape. This is inferred from the code, not tested.

Fix, independent of streaming: if the rollup exists but its ETag
differs from the checkpoint's `RollupETag` (including an empty one),
rebuild the day from all source objects (`ProcessedKeys` plus new ones)
instead of appending. For week/month, treat an existing object as prior.
A rebuild from children is already idempotent. Every option below
inherits this rule.

## Options

### (a) Spool to local ephemeral storage, then upload from the file

Stream decrypt, append and encrypt into a temp file under `/tmp`
(emptyDir), then upload from the file. Spool ciphertext, never
plaintext.

- **Memory:** constant, at the level of age chunks and copy buffers.
- **Disk:** the full object. That is ~1.1 GB per day, ~7.7 GB per week
  and 30–40 GB per month, or about a tenth of that with gzip-in-rollup.
  emptyDir needs `ephemeral-storage` limits above that, and exceeding
  them evicts the pod.
- **Uploads:** it still needs multipart above 5 GiB, so it does not
  solve week or month alone. Below 5 GiB it keeps the single PUT, which
  is the path verified on RGW.
- **Crash safety:** same as today. Uploads are retryable from the file.
- **Size:** ~80 LOC, in `rollup` (temp file instead of `chunkBuffer`)
  and config (`spool-dir`).
- **Use:** as a fallback for days only, if RGW multipart turns out to be
  unusable.

### (b) Multipart upload streamed from a pipe (recommended)

The day producer goroutine writes into an `io.Pipe`. It copies the
existing rollup, GET'd and streamed through `Decrypt`, then calls
`AppendJSONLines` per source object. `Codec.Encrypt` reads the pipe and
writes into a part writer. The part writer buffers one part (16 MiB),
then `UploadPart`s it. Week and month work the same way, with children
streamed in order.

- **Memory:** about (concurrency + 1) × part size. Measured: 16 MiB
  parts, sequential, about 35 MiB heap and 52 MiB RSS. Rollup size does
  not affect it (128 MiB and 800 MiB gave the same result).
- **Disk:** none.
- **Requests:** Create, ⌈size / 16 MiB⌉ × UploadPart, Complete, plus a
  HEAD under ADR 0012. That is ~66 parts per day, ~460 per week and
  ~1,800–2,400 per month. The 10,000-part cap at 16 MiB gives a
  156 GiB maximum. Objects smaller than one part keep the single
  `PutObject`.
- **Encryption:** works with age and none unchanged. `Codec` is already
  `Encrypt(w, r)`/`Decrypt(w, r)` over streams.
- **Crash safety:** nothing is visible before `CompleteMultipartUpload`,
  and the checkpoint is still written after Complete, so ADR 0004 holds.
  - Errors, a no-op (`appended == 0`) and a lost race all abort the
    upload.
  - SIGKILL or OOM leaves an invisible incomplete upload. A bucket
    lifecycle `AbortIncompleteMultipartUpload` rule handles it, and/or a
    sweep using `ListMultipartUploads` under `rollup-prefix`.
  - The conditional goes on Complete: `If-None-Match: *` for a create,
    `If-Match` for an update. With `s3.if-match: false`, HEAD
    immediately before Complete, not before the upload, so the race
    window stays as short as today's.
  - A mid-stream GET failure aborts, and the next cycle retries.
- **ETags:** multipart ETags look like `md5-N`. compactor compares ETags
  opaquely, so this is fine.
- **Size:** about 150 LOC in `storage` plus about 100 LOC in `rollup`,
  plus tests.
  - `S3Store.Put` handles a non-seekable body larger than one part by
    multipart. The `ObjectStore` interface and `MemStore` stay
    unchanged.
  - `rollup` gets the pipe producer for day/week/month and deletes
    `chunkBuffer` and the `encode` copy.
  - Add an ADR that amends 0003, 0004 and 0012.
  - `backfill --no-promote` is no longer needed for memory.
- **Use a hand-rolled writer, not `feature/s3/manager`.** `manager`
  forwards `IfNoneMatch` to Complete (verified). But it sends
  `x-amz-checksum-algorithm: CRC32` and per-part
  `x-amz-checksum-crc32` regardless of the client's `WhenRequired`
  (observed). It also hides Create→Complete, so no HEAD fits in
  between, and it is deprecated in favor of `transfermanager` (v0.x).
  The raw `UploadPart` calls send no checksum headers (observed). The
  scratch prototype writer is about 90 LOC.

### (c) Server-side composition with UploadPartCopy

- **Day append:** copy the existing rollup as part 1, without
  downloading it, then upload only the delta.
- **Week/month:** compose entirely from children, with no data passing
  through the pod.
- **Memory:** one part. Transfer is O(delta), or zero for promotion.
- **Limits:**
  - A copied part must be at least 5 MiB unless it is last. Verified:
    a 1 KiB source as part 1 gives `EntityTooSmall` at Complete. Smaller
    children must be streamed in instead.
  - Each copied part is at most 5 GiB, so a large week needs
    `CopySourceRange` splits.
  - `CopySourceIfMatch` pins the child. Verified: a wrong ETag gives
    412.
  - JSONL children end with `\n`, so concatenation is valid. Verified:
    6 MiB copy plus tail equals the concatenation.
- **age:** not possible. Concatenated age streams do not decrypt
  (verified). Making it work needs a custom multi-segment container with
  a segment index. That breaks `age -d` on rollups and changes
  `materialize`.
- **Plain output only.** It also works for plain gzip rollups, because
  DuckDB reads concatenated gzip members (verified).
- **RGW:** UploadPartCopy is a standard RGW op. Assumed, not verified.
- **Size:** about 150 LOC on top of (b).
- **Use:** only if production output is `none`. It is an optimization,
  not the fix.

### (d) Bounded part objects, with week/month as manifests

The layout becomes `day/2026/01/23/part-NNNN.jsonl[.age]`, with each
part capped (e.g. 256 MiB plaintext).

- **Day run:** rewrite only the tail part with today's algorithm,
  bounded by the cap. Roll over to a new part with
  `If-None-Match: *`, which works on RGW (verified). The day checkpoint
  lists parts with their ETags.
- **Week/month:** these become manifests, or nothing at all.
  - DuckDB can glob a month of plain parts directly
    (`day/2026/01/*/part-*.jsonl`).
  - ISO weeks need an explicit file list.
  - age parts need `materialize` to accept a prefix or manifest and
    stream-decrypt all parts.
- **Memory:** bounded by the part cap.
- **I/O:** O(tail part) per run instead of O(day). Promotion has no data
  rewrite. Today the current day is rewritten on every hourly or
  listener cycle (about 13 GB GET plus 13 GB PUT per day at January
  volume), and the month is rewritten daily.
- **Crash safety:** the part number must come from the checkpoint
  (`len(Parts)`), so a retry overwrites an orphan part `k` instead of
  creating `k+1`. The orphan only holds uncheckpointed events, so glob
  readers see no duplicates. The tail rewrite needs the same
  ETag-mismatch rebuild rule as the Finding, scoped to one part.
- **Migration:** existing single-object days are read as `part-0000`,
  or rewritten. The checkpoint schema version gets a bump, and
  consumers' DuckDB globs and week/month keys change.
- **Size:** about 600–900 LOC across `rollup`, `checkpoint`,
  `materialize`, docs and the ADR.
- **Use:** a later phase, if I/O cost or the listener's rewrite
  frequency hurts.

### Comparison

| | (a) spool | (b) multipart stream | (c) UploadPartCopy | (d) part objects |
|---|---|---|---|---|
| Memory | ~constant | ~2–3 × part (35 MiB measured) | ~1 part | ≤ part cap |
| Ephemeral disk | full object (≤ 40 GB) | none | none | none |
| S3 requests per write | 1 PUT (≤ 5 GiB) | 2 + ⌈size/part⌉ (+HEAD) | 2 + copies + delta parts | 1–2 PUTs per run |
| Transfer per write | O(unit) | O(unit) | O(delta) / 0 | O(tail part) |
| age output | yes | yes | **no** | yes |
| > 5 GiB objects | no | yes | yes | n/a |
| ADR 0004 | unchanged | Complete = commit point | Complete = commit point | per-part, checkpoint-numbered |
| RGW | single PUT verified | MPU unverified | UploadPartCopy unverified | `If-None-Match` verified |
| Code | ~80 LOC | ~250 LOC | +150 LOC on (b) | ~600–900 LOC + migration |
| Layout change | no | no | no | yes |

## Recommendation and phases

0. **RGW probe.** Run against a scratch SysEleven bucket, about half a
   day. Repeat the MinIO checks below: Complete with
   `If-None-Match: *` on existing and absent keys, Complete with a right
   and a wrong `If-Match` (412 is expected, as on PUT), UploadPartCopy
   and `CopySourceIfMatch`, `ListMultipartUploads` and Abort, a
   lifecycle `AbortIncompleteMultipartUpload` rule, and whether parts
   without checksum headers are accepted. The dangerous outcome is RGW
   silently ignoring a condition on Complete. ADR 0012-style HEAD
   emulation covers that case.
1. **Fix the crash-between-writes bug** (Finding) in a separate PR,
   with tests. Streaming builds on its rebuild rule.
2. **(b) multipart streaming** for day, week and month.
   - Use a 16 MiB part constant and sequential uploads; add concurrency
     only if throughput needs it.
   - Abort on error or no-op.
   - Under `s3.if-match: false`, HEAD before Complete; if RGW ignores
     `If-None-Match` on Complete, add a NotFound HEAD check.
   - Document the lifecycle rule. Optionally sweep stale uploads older
     than 24 h at startup.
   - Add metrics for parts and aborted uploads.
   - Write an ADR.
   - Pod memory can then drop to about 256 MiB.
3. **Decide on I/O with data from (b):** gzip inside rollups (pairs
   with #15: about 10x less storage and transfer), (d) part objects,
   or (c) if the output is `none`.
4. **(a)** only as the fallback if Phase 0 shows RGW multipart is
   unusable. It covers days ≤ 5 GiB; week/month would then need (d).

## Verification

### Verified (local, 2026-10-19)

Run against MinIO (`pgsty/minio`) with aws-sdk-go-v2 `service/s3`
v1.114.3, the client options from `S3Store` (path style, checksums
`WhenRequired`), and throwaway programs.

| Check | Result |
|---|---|
| Complete + `If-None-Match: *`, key exists | 412, object unchanged |
| Complete + `If-Match` wrong / right | 412 unchanged / OK; ETag `…-2` |
| `manager.Uploader` forwards `IfNoneMatch` to Complete | yes (412) |
| `manager.Uploader` checksum headers | CRC32 on Create and every part, despite `WhenRequired` |
| Raw UploadPart checksum headers | none |
| UploadPartCopy 1 KiB source as non-last part | `EntityTooSmall` at Complete |
| UploadPartCopy + `CopySourceIfMatch` wrong | 412 |
| UploadPartCopy 6 MiB + uploaded tail | object = byte concatenation |
| `ListMultipartUploads` / Abort | lists the 3 failed uploads; 0 after abort |
| Streamed age day, 16 MiB parts: seed 128 MiB / 700 MiB | peak heap 33 / 35 MiB, RSS 52 / 52 MiB |
| Incremental: GET+decrypt existing → append → re-encrypt → MPU, HEAD before Complete | heap 34–35 MiB; line counts preserved |
| `cat a.age b.age \| age -d` | fails |
| DuckDB `read_ndjson_auto` on concatenated gzip members | reads all rows |
| Crash between rollup and checkpoint (`origin/main`, `MemStore`) | duplicates on update, permanent 412 on create |

### Assumed, not verified

- Ceph RGW (SysEleven) behavior for multipart, conditional Complete,
  UploadPartCopy, lifecycle abort, and `rgw_max_put_size`.
- AWS limits: 5 MiB minimum part (except the last), 5 GiB maximum part,
  10,000 parts, 5 GiB single PUT.
- The week/month crash case, inferred from the code.
- Sustained upload throughput to RGW. A 30 GB month at 50 MB/s takes
  about 10 minutes per cycle.

## Open questions

1. Is production `encryption.scheme` `age` or `none`? If `none`, (c)
   becomes attractive.
2. Is a daily rewrite of a 30–40 GB month (plus the hourly day
   rewrites) acceptable in cost and time, or should (d) or gzip come
   next?
3. Should month rollups include whole boundary ISO weeks
   (`weeksInMonth`)? That duplicates boundary days across two months.
4. Should rollups be gzipped inside age (`.jsonl.gz.age`)? Decide
   together with #15 and #16, since Parquet changes what week/month
   rollups are for.
5. Can the bucket get a lifecycle rule, or must compactor sweep stale
   multipart uploads itself?
