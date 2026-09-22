# 4. Checkpoint-after-rollup ordering with optimistic conditional PUT

## Status

Accepted

## Context

compactor runs as a scheduled job. Two failure modes need handling
without a lock service: (a) a run crashing partway through a rollup
write, and (b) two overlapping/duplicate invocations racing on the
same day/week/month unit.

## Decision

- Checkpoints (`internal/checkpoint`) are small JSON objects stored on
  the same `storage.ObjectStore` as rollups, reusing one auth path and
  one concurrency primitive for both.
- **Ordering**: the rollup object is always written *before* its
  checkpoint. If a run crashes between the two writes, the next run
  simply redoes the same merge — decrypt, append the same "new"
  objects (they're still not in the checkpoint), re-encrypt, overwrite.
  Idempotent, harmless. The reverse ordering (checkpoint first) would
  risk marking events as processed whose rollup write never landed —
  silent data loss.
- **Concurrency**: every rollup/checkpoint write is conditional —
  `IfMatchETag` for an update, `IfNoneMatch: "*"` for a create. A
  `storage.ErrPreconditionFailed` is treated as "another run won this
  cycle" — log and abort this unit, don't retry in a hot loop. The next
  scheduled run catches up naturally.

This is optimistic/best-effort concurrency control, not a strict lock.

## Consequences

- No lock service or leader election needed to run compactor safely
  from, e.g., overlapping cron invocations.
- A `PreconditionFailed` under sustained overlap just means that
  cycle's work is deferred, not lost — the source objects are still
  unprocessed and will be picked up next run.
- If overlapping runs become frequent enough that this causes
  meaningful staleness, a stronger lease/lock mechanism (e.g. a
  `locks/<unit>.lock` object via `IfNoneMatch: "*"`) is a natural
  extension point, deferred at MVP since conditional writes alone were
  sufficient for the target deployment (single scheduled job).
