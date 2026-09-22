# 1. Poll-based discovery behind a pluggable Lister interface

## Status

Accepted

## Context

compactor needs to discover which source event objects under a
`year/month/day/` prefix are new since the last run. Two discovery
strategies exist: poll (`ListObjectsV2` against the prefix each cycle)
or push (S3/MinIO bucket event notifications delivered to a queue).
Push-based discovery scales better and avoids repeatedly listing a
growing prefix, but requires compactor to run as a long-lived listener
rather than a cron-invoked batch job, and MinIO's native notification
support needs to run as a deployment rather than a scheduled job.

## Decision

MVP discovery is poll-based: `storage.PollLister` lists the day's full
prefix on every run and lets `rollup.Engine` filter the result against
its checkpoint of already-processed keys. Discovery is defined behind
`storage.Lister` (`Discover(ctx, prefix, since) ([]SourceEvent, Cursor,
error)`) specifically so a push-based implementation can be added later
without changing `rollup.Engine` or anything above the storage package.

## Consequences

- No queue infrastructure required to run compactor today; it's a
  single scheduled CLI invocation.
- Listing cost grows with the number of objects under a day's prefix,
  which is bounded by the hourly-rollup cadence this project exists to
  enable in the first place.
- Adding a notification-based `Lister` later (e.g. for MinIO) is a new
  file in `internal/storage`, not a rewrite — but that mode implies
  running compactor as a long-lived process, which also brings in the
  liveness/readiness/OTEL requirements for long-running services; that
  work is explicitly deferred.
