package daemon

import (
	"strings"
	"time"
)

// dayFromKey extracts the year/month/day a source object key belongs
// to, given the configured source prefix. Keys that don't match the
// expected "<source_prefix>/YYYY/MM/DD/<file>" layout (e.g.
// notifications for rollup or checkpoint objects, if they ever share
// the bucket) are ignored.
func dayFromKey(key, sourcePrefix string) (time.Time, bool) {
	rest := key
	if sourcePrefix != "" {
		prefix := sourcePrefix + "/"
		if !strings.HasPrefix(key, prefix) {
			return time.Time{}, false
		}
		rest = strings.TrimPrefix(key, prefix)
	}

	parts := strings.SplitN(rest, "/", 4)
	if len(parts) < 4 {
		return time.Time{}, false
	}

	day, err := time.Parse("2006/01/02", strings.Join(parts[:3], "/"))
	if err != nil {
		return time.Time{}, false
	}
	return day, true
}
