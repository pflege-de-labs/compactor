# 8. Decouple source decode scheme from rollup output scheme; batched JSONL sources

## Status

Accepted. Supersedes the homogeneity rule in
[ADR 0002](0002-encryptor-decryptor-abstraction-and-detection.md).

## Context

MVP assumed each source object holds exactly one JSON event, and that
a rollup unit's output scheme should just be whatever single scheme
was detected across its source objects (enforced as a hard error on
mismatch). A real source broke both assumptions: a Redpanda Connect
(Benthos) pipeline batches up to 1000 Keycloak audit events into one
object as JSONL, gzips it, then AES-CTR-encrypts the whole thing with
the IV embedded in the object's filename (not the body, and not an
AEAD — no integrity tag). This is neither "one event per object" nor a
scheme compactor can sensibly *write* (a fixed `KeyExt()` can't encode
a dynamically-generated per-object IV), so treating "detected source
scheme" and "rollup output scheme" as the same value stopped making
sense.

## Decision

**Source decode and rollup output are now separate concerns:**

- `crypto.Decryptor.Decrypt` gained a `key string` parameter (the
  source object's key) so a codec can pull decrypt-time state out of
  the filename instead of the body — needed for the IV-in-filename
  case, harmless (ignored) for codecs that don't need it.
- `crypto.AESCTRGzipCodec` implements this: AES-CTR-decrypt using the
  IV parsed from the key's filename, then gunzip, yielding JSONL.
  `Encrypt` returns an error — compactor never *writes* this format,
  only reads it (source-decode-only codec). Its AES key comes from the
  `COMPACTOR_SOURCE_AES_CTR_GZIP_KEY` env var (hex-encoded), not YAML
  config, so key material never has to live in a config file — the
  same Secret the upstream producer uses can be mounted into
  compactor's pod under that name.
- `rollup.Engine` gained an `OutputScheme` field: the one scheme
  compactor encrypts rollup output with (from `encryption.scheme` in
  config), independent of whichever scheme(s) contributing source
  objects used. Per source object, `Registry.Detect` still picks the
  right codec to *decode* it; those detected schemes no longer need to
  agree with each other. A day/week/month rollup is encrypted with
  `OutputScheme` if *any* source event folded into it was encrypted
  (in *any* scheme, or a mix); otherwise it stays plaintext. A rollup
  already encrypted never downgrades to plaintext, matching ADR 0002's
  original confidentiality-preserving intent — it just no longer
  requires the output scheme to match the source scheme verbatim.
- `rollup.AppendJSONLines` (was `AppendJSONLine`, singular) replaces
  the single-JSON-document assumption: it first tries the whole
  decoded object as one JSON value (so a plain, possibly
  pretty-printed, single-event object still works exactly as before);
  if that fails, it splits on newlines and validates each line
  independently, so one malformed line in a large batch only costs
  that one event, not the other 999.

`rollup.ErrMixedScheme` still exists, now scoped to week/month
promotion only — it fires if children were written with different
*output* schemes (e.g. `encryption.scheme` changed between when they
were rolled up), which is a much rarer, config-drift-shaped situation
than "a data pipeline uses more than one encryption scheme."

## Consequences

- Compactor can consume a real heterogeneous source: same-day objects
  that are plaintext, age-encrypted, and aes-ctr-gzip-encrypted all
  fold into one rollup, encrypted with compactor's own chosen scheme.
- Adding another source-decode-only codec (a different upstream
  producer's format) is a new `crypto.Codec` implementation whose
  `Encrypt` can legitimately be unsupported — no changes needed to
  `rollup.Engine` or the output-side encryption path.
- A day whose output scheme needs to upgrade mid-day (starts
  plaintext, a later event needs `OutputScheme`) writes to a *new* key
  (the extension changes); the old plaintext key is left in place,
  orphaned, rather than deleted — deleting it would add a second
  failure mode (delete succeeds, write fails) to a path that's
  otherwise safely idempotent. Acceptable: this is a rare transition,
  not steady-state behavior.
- `AppendJSONLines`' "whole buffer as one document, else split and
  validate per line" strategy is a pragmatic heuristic, not a formal
  JSONL/single-doc format sniff — it is, however, exactly right for
  both shapes actually seen in practice (a single pretty-printed
  event, or true newline-delimited JSONL), and degrades to "skip the
  one bad line" rather than "lose the whole batch" on any actual
  malformed input.
