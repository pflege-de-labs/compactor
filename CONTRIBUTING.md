# Contributing

Development setup and conventions for humans working on compactor. If
you're an AI coding agent, see [`AGENTS.md`](AGENTS.md) instead (this
file assumes you're not one, though most of it applies either way).

## Prerequisites

- Go 1.27+ (see `go.mod`).
- For local end-to-end testing: a MinIO instance (S3-compatible
  storage) and, for `compactor listen`, a JetStream-enabled NATS
  server (`nats-server -js`, from `brew install nats-server` or
  https://github.com/nats-io/nats-server).

## Building and testing

```sh
go build ./...
go vet ./...
gofmt -l .          # should print nothing
go test ./...
```

All four should be clean before opening a PR. There's no CI wired up
yet (see `docs/architecture.md`'s Deferred section) — these local
commands are the whole check for now.

## Local end-to-end testing

Most logic is covered by unit tests against `storage.MemStore` (an
in-memory `ObjectStore` fake), so you rarely need real infrastructure.
When you do (e.g. testing the NATS consumer or S3 conditional-PUT
behavior against a real backend):

```sh
# MinIO for S3-compatible storage
minio server /tmp/minio-data &
# then point compactor at it: s3.endpoint / --s3-endpoint, s3.use-path-style: true

# JetStream-enabled NATS, for `compactor listen`
nats-server -js -p 4222 &
nats stream add minio-events --subjects "minio.events.>" --storage memory --defaults
```

## Regenerating the man page and completions

```sh
go generate ./...
```

Run this after changing anything under `internal/cli` (flag names,
help text, new commands) and commit the resulting diff under
`docs/man/` and `docs/completions/`.

## Commit conventions

- [Conventional Commits](https://www.conventionalcommits.org/)
  (`feat:`, `fix:`, `docs:`, `refactor:`, ...) for the subject line.
- Explain *why*, not *what* — the diff already shows what changed.
- Keep the subject under ~70 chars; use the body for anything that
  needs more room.

## Code style

- Prefer the standard library over new dependencies; prefer
  actively-maintained dependencies over hand-rolled implementations
  when stdlib doesn't cover it (see `docs/adr/` for examples of this
  tradeoff in practice — e.g. `filippo.io/age` over hand-rolled
  crypto).
- Comments explain *why*, not *what* — one line unless a genuinely
  non-obvious invariant needs more. If it needs more, it usually
  belongs in an ADR instead (see below).
- No speculative abstraction: build what the current requirement
  needs, not what a hypothetical future one might.
- Never put key material in YAML config — see the pattern used for
  `encryption.age.identity_file` (a file path, not the key itself) and
  `COMPACTOR_SOURCE_AES_CTR_GZIP_KEY` (an env var).

## Architectural decisions

Non-obvious design decisions are recorded as ADRs in
[`docs/adr/`](docs/adr/), numbered sequentially
(`adr.github.io` format). If you're changing something an existing ADR
documents, update that ADR's Status (mark it superseded, point at the
new one) rather than rewriting history — see how
[ADR 0002](docs/adr/0002-encryptor-decryptor-abstraction-and-detection.md)
and
[ADR 0008](docs/adr/0008-decouple-source-decode-scheme-from-rollup-output-scheme.md)
handle that. If you're making a new non-obvious call, write one.

## Common extension points

- **New encryption/decryption format**: implement `crypto.Codec`
  (`internal/crypto`) and register it in `internal/cli/app.go`'s
  `buildRegistry`. A source-decode-only format (compactor never writes
  it, only reads it — see `crypto.AESCTRGzipCodec`) can leave `Encrypt`
  returning an error.
- **New bucket-notification transport** (Kafka/AMQP/Redis instead of
  NATS): `internal/notify` is split into transport-agnostic event
  parsing (`event.go`) and the NATS-specific consumer (`nats.go`) —
  add a parallel consumer file; `internal/daemon` doesn't need to
  change.
- **New CLI command**: add a `<Name>Cmd` struct with a `Run(ctx
  context.Context, app *cli.App) error` method (or just `Run(kctx
  *kong.Context) error` if it doesn't need `*App` — see `man`/
  `completions` in `internal/cli/gen.go` for that pattern) and wire it
  into `cli.CLI` in `internal/cli/app.go`.
