package daemon

import (
	"testing"
	"time"
)

func TestDayFromKey(t *testing.T) {
	cases := []struct {
		name         string
		key          string
		sourcePrefix string
		wantOK       bool
		wantDay      string
	}{
		{"with prefix", "source/2026/09/22/e.json", "source", true, "2026-09-22"},
		{"no prefix configured", "2026/09/22/e.json", "", true, "2026-09-22"},
		{"wrong prefix", "other/2026/09/22/e.json", "source", false, ""},
		{"rollup key shape, no prefix configured", "rollups/day/2026/09/22.jsonl", "", false, ""},
		{"too short", "source/2026/09", "source", false, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			day, ok := dayFromKey(tc.key, tc.sourcePrefix)
			if ok != tc.wantOK {
				t.Fatalf("dayFromKey(%q, %q) ok = %v, want %v", tc.key, tc.sourcePrefix, ok, tc.wantOK)
			}
			if ok && day.Format("2006-01-02") != tc.wantDay {
				t.Fatalf("dayFromKey(%q, %q) day = %v, want %v", tc.key, tc.sourcePrefix, day.Format(time.DateOnly), tc.wantDay)
			}
		})
	}
}
