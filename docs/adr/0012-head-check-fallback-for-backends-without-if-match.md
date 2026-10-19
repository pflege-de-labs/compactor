# 12. HEAD-check fallback for backends without If-Match

## Status

Accepted. Amends [ADR 0004](0004-checkpoint-and-conditional-put-concurrency-model.md).

## Context

ADR 0004 makes every update a PUT with `If-Match`. SysEleven's object
storage (Ceph RGW) answers such a PUT with `412 PreconditionFailed` even
when the ETag matches. Verified on 2026-10-19 with compactor itself
(no checksum trailer, current ETag): the same update succeeded with the
condition dropped. `If-None-Match: *` works there. Every update of an existing day rollup or checkpoint
therefore failed, and the day never advanced past its first run.

## Decision

`s3.if-match` (default `true`) controls whether updates send `If-Match`.
With `false`, `storage.S3Store.Put` HEADs the key first, returns
`ErrPreconditionFailed` if it is missing or its ETag differs, and then
PUTs without a condition. Creates still use `If-None-Match: *`.

## Consequences

- compactor runs on RGW-backed storage.
- The update is no longer atomic: a write landing between HEAD and PUT
  is overwritten. This is safe only with a single writer per unit: run
  CronJobs with `concurrencyPolicy: Forbid` and do not run the listener
  alongside an overlapping hourly job.
- Backends that honor `If-Match` keep the default and the ADR 0004
  guarantees. A lease object via `If-None-Match: *` (ADR 0004's
  extension point) would restore safety for concurrent writers on RGW.
