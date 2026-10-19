# 13. Rebuild a day when its rollup and checkpoint disagree

## Status

Accepted. Amends [ADR 0004](0004-checkpoint-and-conditional-put-concurrency-model.md).

## Context

ADR 0004 writes the rollup before its checkpoint and claims a run that
crashes in between is harmless because the next run redoes the merge.
It does not (issue #27):

- An update retry reads the already updated day rollup back and appends
  the same source events again: silent duplicates.
- A create retry sends `If-None-Match: *` for the object the crashed run
  left behind and fails with 412 on every run until `checkpoint reset`.
  Week and month creation behaved the same.
- A day rollup deleted while its checkpoint remained was rewritten with
  only the new events, dropping the rest.

## Decision

- The day checkpoint's `rollup_etag` is the contract. If the existing
  day rollup's ETag differs, or the rollup is missing although the
  checkpoint names one, the day is rebuilt from all its source objects
  instead of appended to.
- Every rollup write is conditioned on the object found at the target
  key: `If-Match` on the known or HEADed ETag, `If-None-Match: *` only if
  nothing is there. An object the checkpoint doesn't know comes from a
  crashed run whose content the retry covers, so overwriting it is safe.
  Week and month promotions always rebuild from their children, so they
  always overwrite.

## Consequences

- Retries after a crash converge to the correct rollup.
- A rebuild needs every source object of the day; with source retention
  shorter than the rollup's lifetime, a mismatch on an old day loses the
  events whose sources are gone.
- Two overlapping runs no longer fail the second create with 412; the
  conditional checkpoint write still decides which one wins, and a
  rollup overwritten after the winner's checkpoint shows up as a
  mismatch and is rebuilt on the next run.
- Deleting a day rollup (keeping its checkpoint) is now a way to force
  a rebuild of that day.
