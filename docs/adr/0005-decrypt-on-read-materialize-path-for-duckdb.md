# 5. Decrypt-on-read materialize path for DuckDB, no plaintext mirror

## Status

Accepted

## Context

DuckDB can query JSONL directly but has no generic decryption support
for arbitrary ciphertext (only native Parquet encryption, which
doesn't apply here). Rollups that came from encrypted source data must
stay encrypted at rest, so DuckDB needs a way to read them without
compactor ever maintaining a standing plaintext copy of sensitive data.

## Decision

`internal/materialize` decrypts a single rollup object into a local
scratch file on demand (`compactor materialize <key>`), writes it
`0600`, and prints its path so it composes with an external DuckDB
invocation, e.g.:

```
duckdb -c "SELECT * FROM read_ndjson_auto('$(compactor materialize rollups/day/2026/09/22.jsonl.age)')"
```

The command does not delete the file on exit — the caller (DuckDB)
needs it to still exist after the command substitution completes.
Instead, a TTL sweep (`materialize.cache_ttl`, default 1h) run at the
start of the *next* `materialize` invocation reclaims stale scratch
files, so plaintext doesn't accumulate indefinitely under normal use.
A library-level `cleanup func()` is also returned by `Materialize` for
future in-process callers (e.g. a deferred `compactor query` wrapper
that execs DuckDB itself and can clean up immediately after).

## Consequences

- Ciphertext never reaches DuckDB directly; the confidentiality
  boundary is crossed exactly once, deliberately, per query.
- There's a real (bounded, TTL-limited) window where decrypted data
  sits on local disk. This is called out explicitly rather than
  glossed over — operators running compactor against sensitive data
  should point `materialize.cache_ttl`/the scratch dir at storage with
  appropriate access controls.
- No permanent plaintext mirror is maintained, avoiding a second copy
  of sensitive data to secure and keep in sync.
