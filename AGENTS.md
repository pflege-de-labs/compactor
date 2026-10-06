# AGENTS.md

Instructions for AI coding agents working in this repository. Humans:
see [`CONTRIBUTING.md`](CONTRIBUTING.md) instead (some of this
overlaps, but that file is written for you).

## What this is

compactor rolls up per-event JSON/JSONL objects in an S3-compatible
bucket into day/week/month JSONL rollups, preserving client-side
encryption end-to-end. Read
[`docs/architecture.md`](docs/architecture.md) before making
non-trivial changes — it's short and explains how the packages fit
together. Non-obvious design decisions are recorded as ADRs in
[`docs/adr/`](docs/adr/); read the relevant ones before touching
`internal/rollup`, `internal/crypto`, or `internal/checkpoint`, since
several behaviors that look like they could be simplified are
deliberate (see "Invariants" below).

## Before considering a change done

```sh
go build ./...
go vet ./...
gofmt -l .          # must print nothing — gofmt -w if it doesn't
go test ./...
```

All four must be clean, and CI additionally enforces `go mod tidy`
cleanliness, `govulncheck` and `gosec` (see `CONTRIBUTING.md` for the
commands). If you touched `internal/cli` (flag names, help text, new
commands), also run `go generate ./...` and include the resulting diff
under `docs/man/` and `docs/completions/` — CI fails when they are
stale (the man page's `.TH` date line is ignored). The `gosec` check
is clean; fix real findings and annotate intentional ones with
`#nosec Gxxx -- <reason>`.

Chart changes (`charts/compactor`) need `helm lint` and a render of every
`ci/*-values.yaml`; when you add a value or template, add or extend a
`ci/` values file and, if behavior is non-obvious, an assertion in the
`chart` job of `.github/workflows/ci.yml`. Config keys in the chart's
`config:` tree mirror `config.example.yaml` (dashed keys).

Don't hand-bump GitHub Action pins, the Dockerfile base images or
Go dependencies purely for freshness: Renovate owns them. CI/CD
decisions are in `docs/adr/0009-ci-cd-pipeline.md`.

Prefer verifying behavior empirically over trusting a package's docs
or your own recollection of its API, especially for less-common
libraries (kong hooks/bindings, mango-kong, miekg/king, the NATS
JetStream client) — this project's history includes real bugs (e.g. a
`context.Context` binding that silently never worked) that only
surfaced under an actual run, not `go build`/`go vet`. A local
`nats-server -js` and a local MinIO are cheap to spin up for this; see
`CONTRIBUTING.md`.

## Conventions already established in this repo

- **Language/deps**: Go, stdlib preferred, well-maintained deps over
  hand-rolled implementations when stdlib doesn't cover it.
- **CLI**: `kong` for parsing, `kong-yaml` for config (every setting
  is both a CLI flag and a YAML key — see
  `docs/architecture.md`'s Configuration section for how the
  `embed:"" prefix:"..."` + dash-joined-key resolution works),
  `mango-kong` + `miekg/king` for man page/completions (as real
  subcommands, `internal/cli/gen.go` — not a separate tool), XDG
  paths via `adrg/xdg`.
- **Observability**: `compactor listen` (the long-running mode)
  exposes `/healthz`+`/readyz`; both modes report OTEL metrics via
  `autoexport`, configured only through standard `OTEL_*` env vars.
- **Commits**: Conventional Commits, explain *why* not *what*. Do not
  commit unless the user explicitly asks. Prefer new commits over
  amending, except when the user asks for an amend or the repo has an
  unpushed single initial commit still being iterated on.
- **Comments**: one line, WHY not WHAT. If it needs more than a line,
  it's probably ADR material instead.
- **No speculative abstraction**: build what's asked, not a
  hypothetical future generalization of it.
- **Never put key material in YAML config** — env vars or a file path
  referenced by config only (see `encryption.age.identity_file`,
  `COMPACTOR_SOURCE_AES_CTR_GZIP_KEY`).

## Invariants worth knowing before editing `internal/rollup` or `internal/crypto`

- A rollup, once encrypted, never gets rewritten as plaintext — even
  if a run's new source events happen to all be plaintext. See
  `Engine.OutputScheme` and ADR 0002/0008.
- Source objects may mix encryption schemes (or plaintext and
  encrypted) freely within one day; the rollup's *output* scheme is a
  single configured value, decoupled from source scheme. Don't
  reintroduce a "source objects must share one scheme" check — that
  was deliberately removed (ADR 0008) after a real source turned out
  to need it.
- The rollup object is always written *before* its checkpoint (ADR
  0004) — a crash between the two just means the next run redoes an
  idempotent merge. Reversing that order risks marking events
  processed whose rollup write never landed.
- Every rollup/checkpoint write is a conditional PUT
  (`IfMatchETag`/`IfNoneMatch`). A `storage.ErrPreconditionFailed`
  should be logged and treated as "another run won this cycle," not
  retried in a hot loop.
- `rollup.AppendJSONLines` tries the whole decoded object as one JSON
  value first, falling back to per-line validation. Don't replace this
  with a naive `bytes.Split(raw, "\n")` — that breaks pretty-printed
  single-event sources. Don't replace it with a single streaming
  `json.Decoder` loop either — that can't resync past one bad line in
  a large batch (this was tried and reverted; see ADR 0008 and the
  `TestAppendJSONLines_OneBadLineDoesNotSinkTheRestOfALargeBatch`
  test).

## Where to look first

- `docs/architecture.md` — package layout and data flow.
- `docs/adr/` — why things are the way they are.
- `internal/rollup/engine.go` + `promote.go` — the core algorithm.
- `internal/rollup/engine_test.go` — the algorithm's actual behavior,
  demonstrated (including the "why" cases: mixed schemes, encrypted
  round-trip, JSONL batches).
