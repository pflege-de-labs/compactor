package rollup_test

import (
	"bytes"
	"testing"

	"github.com/pflege-de-labs/compactor/internal/rollup"
)

func TestAppendJSONLines(t *testing.T) {
	cases := []struct {
		name         string
		raw          string
		wantAppended int
		wantSkipped  int
	}{
		{
			name:         "single pretty-printed document",
			raw:          "{\n  \"id\": 1,\n  \"event\": \"login\"\n}",
			wantAppended: 1,
			wantSkipped:  0,
		},
		{
			name:         "true JSONL batch",
			raw:          "{\"id\":1}\n{\"id\":2}\n{\"id\":3}\n",
			wantAppended: 3,
			wantSkipped:  0,
		},
		{
			name:         "JSONL with a malformed line then a valid trailing line",
			raw:          "{\"id\":1}\nnot json\n{\"id\":2}\n",
			wantAppended: 2,
			wantSkipped:  1,
		},
		{
			name:         "empty input",
			raw:          "",
			wantAppended: 0,
			wantSkipped:  0,
		},
		{
			name:         "whitespace-only input",
			raw:          "  \n\n  ",
			wantAppended: 0,
			wantSkipped:  0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			appended, skipped := rollup.AppendJSONLines(&buf, []byte(tc.raw))
			if appended != tc.wantAppended || skipped != tc.wantSkipped {
				t.Fatalf("AppendJSONLines(%q) = (appended=%d, skipped=%d), want (appended=%d, skipped=%d)",
					tc.raw, appended, skipped, tc.wantAppended, tc.wantSkipped)
			}
			if appended > 0 {
				lines := nonEmptyLines(buf.Bytes())
				if len(lines) != appended {
					t.Fatalf("buffer has %d non-empty lines, want %d matching appended count", len(lines), appended)
				}
			}
		})
	}
}

func TestAppendJSONLines_TrailingGarbageOnOneLineIsJustThatLineSkipped(t *testing.T) {
	// Not valid as one whole-buffer JSON value (trailing garbage), and
	// as a single "line" (no newline) it's not valid JSON either -
	// skipped, but doesn't affect anything else in the buffer.
	var buf bytes.Buffer
	appended, skipped := rollup.AppendJSONLines(&buf, []byte(`{"id":1} not-json-at-all`))
	if appended != 0 || skipped != 1 {
		t.Fatalf("got (appended=%d, skipped=%d), want (appended=0, skipped=1)", appended, skipped)
	}
}

func TestAppendJSONLines_OneBadLineDoesNotSinkTheRestOfALargeBatch(t *testing.T) {
	var raw bytes.Buffer
	for i := range 1000 {
		if i == 500 {
			raw.WriteString("this line is corrupt\n")
			continue
		}
		raw.WriteString(`{"id":`)
		raw.WriteString("1}\n")
	}

	var buf bytes.Buffer
	appended, skipped := rollup.AppendJSONLines(&buf, raw.Bytes())
	if appended != 999 || skipped != 1 {
		t.Fatalf("got (appended=%d, skipped=%d), want (appended=999, skipped=1)", appended, skipped)
	}
}
