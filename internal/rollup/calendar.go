package rollup

import "time"

type isoWeekRef struct {
	year, week int
}

// daysInISOWeek returns the 7 calendar days (Monday-Sunday, UTC)
// belonging to the given ISO 8601 week.
func daysInISOWeek(isoYear, isoWeek int) []time.Time {
	// Jan 4th is always in ISO week 1 of its year.
	jan4 := time.Date(isoYear, time.January, 4, 0, 0, 0, 0, time.UTC)
	weekday := int(jan4.Weekday())
	if weekday == 0 { // Go's Sunday == 0; ISO treats Sunday as day 7
		weekday = 7
	}
	week1Monday := jan4.AddDate(0, 0, -(weekday - 1))
	monday := week1Monday.AddDate(0, 0, (isoWeek-1)*7)

	days := make([]time.Time, 7)
	for i := range days {
		days[i] = monday.AddDate(0, 0, i)
	}
	return days
}

// weeksInMonth returns the distinct ISO weeks touched by any day of the
// given calendar month, in chronological order.
func weeksInMonth(year int, month time.Month) []isoWeekRef {
	first := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	last := first.AddDate(0, 1, -1)

	seen := make(map[isoWeekRef]bool)
	var weeks []isoWeekRef
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		y, w := d.ISOWeek()
		ref := isoWeekRef{year: y, week: w}
		if !seen[ref] {
			seen[ref] = true
			weeks = append(weeks, ref)
		}
	}
	return weeks
}
