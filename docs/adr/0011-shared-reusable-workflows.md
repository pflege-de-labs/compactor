# 11. CI and release jobs come from the shared pflege-de-labs workflows

## Status

Accepted. Amends [ADR 0009](0009-ci-cd-pipeline.md).

## Context

ADR 0009 adopted the pipeline teamster and tranquila had settled on, and compactor kept its own
copy of every job. The copies drift: action pins differ by a patch, and a fix in one repository
has to be repeated in the others by hand.
[pflege-de-labs/github-workflows](https://github.com/pflege-de-labs/github-workflows) now holds
those jobs as reusable workflows, extracted from these repositories.

## Decision

`ci.yml`, `release.yml` and `release_helm_chart.yml` call the shared workflows, pinned to a commit
sha with the release tag in a comment, which Renovate moves like any other action pin. Every
decision in ADR 0009 stands; only where the steps are written changes.

- `go-checks.yml` is gone; the shared `go-checks` runs the same build, vet, gofmt, tests with
  reported coverage, tidy, `go generate` drift (ignoring the man page's `.TH` line), govulncheck
  and gosec, in CI and again on the tagged commit.
- The chart's render assertions moved from an inline step to `scripts/check-chart-render.sh`,
  which the shared `helm-lint` runs after linting and rendering every `ci/*-values.yaml`.
- The SecObserve upload is its own job after the image, in CI and in a release. A failure to read
  the SBOM attestation is now a warning like the upload itself, not a failed build.

## Consequences

- Signatures made by the shared workflows name the template, not compactor's workflow. Verify
  with the template identity and pin the repository through the workflow-repository claim (see
  the README). Releases up to v0.1.0 keep the old identity.
- A fix to a shared step reaches compactor through a Renovate pin bump.
- Changing a shared step means a release of github-workflows; a compactor-only step goes into a
  script here, as the chart assertions did.
- Check names in the Actions UI gain the caller's job as a prefix, e.g. `checks / Build & Test`.
