# 3. Incremental day rollups, rebuild-from-children for week/month

## Status

Accepted

## Context

AEAD ciphers (used by every codec compactor supports, including age)
require a fresh nonce per encryption operation, so ciphertext cannot
be appended to in place. Any "append" operation on an encrypted rollup
must actually be: decrypt whole file, append plaintext, re-encrypt
whole file, overwrite. This is cheap as long as the file being
rewritten stays small — true for a single day's rollup, but a naive
extension of the same "append new leaf objects" pattern one level up
would mean re-decrypting/re-encrypting an entire week's or month's
worth of data every time a single new day arrives.

## Decision

Two different strategies at two different levels:

- **Day level** is incremental: `Engine.RunHourlyDayRollup` decrypts
  the existing day rollup (if any), appends only the new source
  objects not yet in the day's checkpoint, re-encrypts, and does a
  conditional overwrite. Cost is bounded by one day's data.
- **Week/month levels are rebuilt from children**, not incrementally
  appended: `RunPromoteWeek`/`RunPromoteMonth` decrypt each present
  child (day rollups for a week, week rollups for a month), concatenate
  in chronological order, re-encrypt, and overwrite. A short-circuit
  compares each child's ETag against what was folded in last time;
  if nothing changed, the cycle is a no-op and skips the rewrite
  entirely.

## Consequences

- No cross-level delta-tracking is needed — week/month promotion is
  simple and self-correcting (a rerun after a day rollup was
  corrected/backfilled just picks up the new child ETag next cycle).
- Every week/month promotion is O(week or month size) when it does
  write, same cost class as a day rollup rewrite, just larger. This is
  accepted as fine at the data volumes this project targets; if it
  stops being fine, the day-level "existing rollup + delta" pattern
  could be extended upward at the cost of real complexity.
- A day/week that never gets a rollup (e.g. genuinely zero events, or
  stuck on `ErrMixedScheme`) is simply skipped when its parent is
  promoted — the parent includes whatever children exist and revisits
  the rest on a later cycle.
