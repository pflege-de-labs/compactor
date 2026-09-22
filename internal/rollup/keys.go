package rollup

import (
	"fmt"
	"time"

	"github.com/pflege-de-labs/compactor/internal/crypto"
)

// DayPrefix returns the "year/month/day" prefix source events for t live
// under, matching the bucket layout compactor consumes.
func DayPrefix(t time.Time) string {
	return t.Format("2006/01/02")
}

// DayRollupKey, WeekRollupKey and MonthRollupKey all derive their
// extension from codec.KeyExt(), which is "" for the no-op codec — so a
// day's rollup lives at the same key for its whole lifetime as long as
// its detected scheme doesn't change (enforced by the homogeneity rule
// in rollup.Engine).
func DayRollupKey(rollupPrefix string, day time.Time, codec crypto.Codec) string {
	return fmt.Sprintf("%s/day/%s.jsonl%s", rollupPrefix, day.Format("2006/01/02"), codec.KeyExt())
}

func WeekRollupKey(rollupPrefix string, isoYear, isoWeek int, codec crypto.Codec) string {
	return fmt.Sprintf("%s/week/%04d-W%02d.jsonl%s", rollupPrefix, isoYear, isoWeek, codec.KeyExt())
}

func MonthRollupKey(rollupPrefix string, year int, month time.Month, codec crypto.Codec) string {
	return fmt.Sprintf("%s/month/%04d-%02d.jsonl%s", rollupPrefix, year, int(month), codec.KeyExt())
}

// DaysToProcess returns "now" (UTC day) plus the stragglerDays prior
// days, newest first, so a scheduled hourly run also catches events
// that arrived late against an already-rolled-up day.
func DaysToProcess(now time.Time, stragglerDays int) []time.Time {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	days := make([]time.Time, 0, stragglerDays+1)
	for i := 0; i <= stragglerDays; i++ {
		days = append(days, today.AddDate(0, 0, -i))
	}
	return days
}
