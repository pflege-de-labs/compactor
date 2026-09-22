# 2. Pluggable Encryptor/Decryptor abstraction with per-object detection

## Status

Accepted. The **homogeneity rule** below (source objects within a
rollup unit must share one detected scheme, enforced as a hard error)
is **superseded by
[ADR 0008](0008-decouple-source-decode-scheme-from-rollup-output-scheme.md)**:
source objects may now mix schemes freely; only the rollup's own
output scheme is single-valued. Everything else on this page
(the `Encryptor`/`Decryptor`/`Codec`/`Registry` abstraction itself,
extension+sniff detection) remains as designed.

## Context

Source event files are client-side encrypted with a non-KMS scheme
(age, at MVP), but not uniformly — some objects/days are plaintext.
The exact scheme in use by upstream producers may also evolve. Rollup
output must preserve whatever confidentiality the source data had: it
must never silently downgrade encrypted input to a plaintext rollup,
and it must use the correct scheme to re-encrypt.

## Decision

`internal/crypto` defines `Encryptor`/`Decryptor`/`Codec` interfaces
and a `Registry` that looks up a codec by `Scheme` and detects an
object's scheme from its key's extension (`Codec.KeyExt()`) with a
content-header sniff (`Sniffer.Sniff`) as fallback. MVP ships two
codecs: `NoopCodec` (`Scheme() == "none"`) and `AgeCodec` (wraps
`filippo.io/age`). Config is namespaced per scheme
(`encryption.scheme`, `encryption.age.*`) so adding a backend (PGP,
static-key AES-GCM) is additive, not a schema break.

**Homogeneity rule (superseded by ADR 0008 — kept here for history):**
every rollup unit (one day's new events, or one week/month's children)
was originally required to resolve to a single detected scheme, a
mismatch being a hard error (`rollup.ErrMixedScheme`). In practice, a
real source (a batching pipeline writing a non-`age` format) made this
too strict: source objects legitimately mix plaintext and encrypted
(or different encrypted schemes) within a day. ADR 0008 decouples
source decode scheme (per-object, still detected here) from rollup
output scheme (one fixed, configured scheme, applied whenever any
source event needed it) — see that ADR for the current rule.

## Consequences

- Encryption logic never appears in `rollup.Engine` as scheme-specific
  branches — it's always "get the codec for this scheme, call
  Encrypt/Decrypt".
- A day whose source events are genuinely mixed-scheme (e.g. a
  producer mid-migration) stalls that day's rollup until fixed, rather
  than corrupting or downgrading it. This is a deliberate fail-closed
  choice.
- `DayCheckpoint.Scheme` (and the equivalent field on week/month
  checkpoints) records the scheme a rollup was last written with, so
  subsequent runs can locate the existing rollup object (whose key
  embeds the codec's extension) without re-detecting from scratch.
