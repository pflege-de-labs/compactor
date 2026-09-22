package rollup

import (
	"bytes"
	"encoding/json"
)

// AppendJSONLines decodes raw as either a single JSON document (which
// may be pretty-printed, i.e. contain embedded newlines) or a batch of
// newline-delimited JSON events (JSONL) — a source object may be
// either, depending on the producer — and appends each well-formed
// event to buf as one compacted line.
//
// raw is first tried as a single JSON value spanning the whole buffer;
// if that succeeds it's treated as one event, which keeps a plain
// single-event (optionally pretty-printed) source object working.
// Otherwise raw is split on newlines and each non-empty line is
// validated independently, so one malformed line in a large batch
// doesn't cost the rest of it — a producer can emit up to (e.g.) 1000
// events per object, and losing 999 good ones to 1 bad one would be a
// disproportionate failure mode.
func AppendJSONLines(buf *bytes.Buffer, raw []byte) (appended, skipped int) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return 0, 0
	}

	if json.Valid(trimmed) {
		var compacted bytes.Buffer
		if err := json.Compact(&compacted, trimmed); err == nil {
			buf.Write(compacted.Bytes())
			buf.WriteByte('\n')
			return 1, 0
		}
	}

	for line := range bytes.SplitSeq(raw, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		var compacted bytes.Buffer
		if err := json.Compact(&compacted, line); err != nil {
			skipped++
			continue
		}
		buf.Write(compacted.Bytes())
		buf.WriteByte('\n')
		appended++
	}
	return appended, skipped
}
