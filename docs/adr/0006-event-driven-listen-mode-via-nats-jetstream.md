# 6. Event-driven `listen` mode via NATS JetStream, reusing the existing reconcile-by-listing core

## Status

Accepted

## Context

The MVP (ADR 0001) discovers new source objects by polling
`ListObjectsV2` on a schedule. Running compactor as a Kubernetes
Deployment for near-real-time rollups instead means reacting to bucket
notifications. MinIO can publish its S3-compatible event notifications
to several targets (webhook, Kafka, NATS, AMQP, Redis, ...); NATS
JetStream was chosen for this project (durable, low-ops, single Go
client dependency).

A tempting design would make the notification stream the source of
truth — parse each event, and directly append that one object into the
day rollup. That's fragile: broker message loss, redelivery, or a
backfill uploaded outside the notification path would silently miss
events, since nothing double-checks against reality.

## Decision

Notifications are used purely as a **trigger**, not as a source of
truth. `internal/notify` parses MinIO's S3 event JSON (shared schema
across all its notification targets) off a NATS JetStream durable pull
consumer and extracts `(bucket, key)`. `internal/daemon` maps each
key's `year/month/day` to a day and debounces bursts
(`daemon.Debouncer`, default 5s) into a single call to
`rollup.Engine.RunHourlyDayRollup` for that day — the exact same
reconcile-by-listing-and-diffing-against-checkpoint logic the
scheduled CronJob mode uses (ADR 0001). A dropped or duplicate
notification just means that day reconciles slightly later or an extra
time; it can never cause a missed event, because the actual truth
still comes from listing the day's prefix and diffing against the
checkpoint.

`compactor listen` (the Deployment entrypoint) also serves
`/healthz` (always 200 once the process is up) and `/readyz` (200 once
the JetStream consumer is connected and consuming) for Kubernetes
probes, and shuts down cleanly on SIGTERM/SIGINT.

## Consequences

- `storage.Lister`/`PollLister` and `rollup.Engine` needed zero
  changes to support event-driven mode — only a new trigger source was
  added above them, exactly as anticipated in ADR 0001.
- Notification delivery guarantees only need to be "good enough to
  reconcile promptly," not "exactly once" or "never lost" — much
  cheaper to operate.
- A day with heavy notification traffic (e.g. many small event files
  landing at once) reconciles once per quiet period rather than once
  per notification, bounding load on the object store from the event
  path the same way the hourly cadence bounds it in poll mode.
- The stream/NATS-side MinIO notification target configuration is
  operational setup (provisioned alongside MinIO), not something
  compactor manages — `compactor listen` only creates/reuses its own
  durable consumer on an already-existing stream.
