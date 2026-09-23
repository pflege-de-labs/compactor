# Security Policy

## Supported versions

compactor is pre-1.0 and does not yet maintain release branches.
Security fixes are made against `main` only; there is no backport
policy until a first tagged release exists.

## Reporting a vulnerability

Please report suspected vulnerabilities privately — **do not open a
public GitHub issue**.

Email **jens.hausherr@pflege.de** with:

- A description of the issue and its potential impact.
- Steps to reproduce (a minimal example, if possible).
- Any relevant logs, config, or affected version/commit.

You should expect an acknowledgement within 5 business days. We'll
work with you on a fix and coordinate a disclosure timeline before any
public write-up.

## Scope notes specific to this project

compactor decrypts and re-encrypts event data that may be sensitive
(e.g. audit logs). A few areas particularly worth flagging if you find
an issue:

- **Key handling**: encryption key material is only ever accepted via
  environment variables or files referenced by config (age identity
  file, `COMPACTOR_SOURCE_AES_CTR_GZIP_KEY`), never inline in YAML —
  if you find a path where key material ends up logged, written to a
  checkpoint/rollup object, or otherwise persisted outside its
  intended location, that's a vulnerability.
- **Materialized (decrypted) scratch files**: `compactor materialize`
  and `internal/materialize` write decrypted content to local disk
  (`0600`, TTL-swept). A bug that widens those file permissions, skips
  the TTL sweep, or leaks the scratch path is in scope.
- **Confidentiality downgrade**: compactor is designed to never write
  a plaintext rollup when its source events were encrypted (see
  `docs/adr/0002-encryptor-decryptor-abstraction-and-detection.md` and
  `docs/adr/0008-decouple-source-decode-scheme-from-rollup-output-scheme.md`).
  Any code path that silently drops encryption is a vulnerability, not
  just a bug.

General dependency vulnerabilities (e.g. a CVE in a Go module this
project depends on) are also welcome as reports, though `go.sum` churn
from routine dependency updates doesn't need a security report — a
normal issue or PR is fine for those.
