# 9. CI builds and signs an image per commit; releases rebuild, re-gate and sign

## Status

Accepted

## Context

compactor ships as a container image (run as a Deployment for `listen`
and a CronJob for `rollup hourly`) and as standalone binaries. It needs
a CI/CD pipeline that gates artifacts on the checks, leaves supply-chain
evidence consumers can verify, and keeps dependencies current.
`pflege-de-labs/teamster` and `pflege-de-labs/tranquila` already settled
on one pattern after hitting its failure modes; tranquila records the
reasoning in its ADR 0001 (CI/CD pipeline) and ADR 0003 (dependency
update policy). This record adopts that pattern rather than
re-deriving it, and notes only where compactor differs.

## Decision

- **The image build lives in `ci.yml`**, gated by `needs: checks` on a
  reusable `go-checks.yml`, so no image exists for a commit whose checks
  failed while `docker/metadata-action` still sees the real branch/PR
  event context. Triggers are `pull_request`, `push: main` and
  `workflow_dispatch`. Fork PRs build but neither push nor sign (no
  credentials, no OIDC token). CI images are tagged `<short-sha>`,
  `<branch>` and `pr-<n>` — never `:latest`.
- **One reusable `go-checks.yml`** is called by both CI and release so
  the gate cannot drift: build, vet, `gofmt`, tests with coverage,
  `go mod tidy` and `go generate` cleanliness, `govulncheck` and `gosec`.
  `setup-go` reads `go.mod`, so CI builds with the toolchain the
  Dockerfile pins. The `go generate` check ignores the man page's `.TH`
  line, which embeds the generation date and would otherwise fail daily.
- **Releases rebuild, they do not retag.** A `v*` tag runs `guard`
  (reachable from `main`) → `verify` (the same `go-checks.yml`) →
  `image` → `binaries`. Because the release rebuilds, the gate has to
  run again on the tagged commit rather than trusting main's last CI
  run. `VERSION` is the tag; the image job asserts `--version` reports
  it, since `-X main.version` silently no-ops on a wrong symbol path.
- **`:latest` follows the highest stable tag**, not "the tip of main":
  Renovate automerges, so main can move between the tag push and the
  run, which would silently drop `:latest`. A tag with a `-` suffix is a
  pre-release everywhere (image tags and the GitHub release) and never
  moves `:latest`.
- **Supply-chain evidence**: keyless `cosign sign` by digest (the
  signing identity is the workflow's OIDC token, so there is no key to
  hold), buildx SBOM and, for releases, `provenance: mode=max`
  attestations on the index, and a signed `checksums.txt` covering the
  release binaries and their SPDX SBOMs. CI sets `provenance: false`
  because the default mode=min provenance adds an extra manifest that
  GHCR renders as `unknown/unknown` while asserting nothing the
  signature does not already prove.
- **SecObserve is a findings tool, not a gate.** The SBOM upload and
  Trivy scan run for `main`/`workflow_dispatch` builds and releases
  (never PR builds, so transient branches leave no trace), are
  `continue-on-error`, and report failures as a warning in the job
  summary. The scanner image is run directly rather than through
  upstream's `upload_sbom` action, whose entrypoint cannot report
  failure; its pin is kept current by a Renovate custom manager.
  Requires the `SO_API_TOKEN` secret.
- **Coverage is reported, not gated.** The convention is to enforce
  coverage above 75%, but compactor starts far below it (roughly:
  `crypto` 73%, `rollup` 58%, `daemon` 45%, `notify` 25%, `storage`
  23%, and `cli`/`config`/`checkpoint`/`metrics` untested), so a gate
  today would fail every PR and release. The total is written to the
  job summary instead; a gate is to be introduced once tests catch up.
- **Renovate** (`.github/workflows/renovate.yml`, `.github/renovate.json`)
  pins Actions to commit SHAs, automerges Go minor/patch and Action
  digest/minor/patch bumps once CI passes, and holds majors, the Go
  toolchain and runtime base images for a person. Modules that must move
  together are grouped (AWS SDK; OpenTelemetry) as in tranquila's ADR
  0003. Requires the `RENOVATE_TOKEN` secret, because PRs opened with the
  workflow token do not trigger CI and automerge waits on it.

## Consequences

- A broken `go generate`, `go mod tidy` or formatting state is caught in
  CI instead of surfacing in a later unrelated change.
- A release takes the full check suite plus a two-platform build before
  anything is published, in exchange for the artifacts being exactly what
  the tag contains.
- Coverage can regress unnoticed until the gate exists; the summary line
  makes that visible but not blocking.
- The Helm chart is published by its own workflow and linted/rendered by
  a `chart` job in `ci.yml`; see [ADR 0010](0010-helm-chart.md).
- Action pins are Renovate-managed; do not bump them by hand.
