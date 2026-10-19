package cli

import (
	"fmt"
	"testing"
)

func TestBackfillUnits_SpansYearAndISOWeekBoundary(t *testing.T) {
	// 2026-12-31 is in ISO week 2026-W53, 2027-01-04 starts 2027-W01.
	days, weeks, months, err := backfillUnits("2026-12-30", "2027-01-04")
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 6 || days[0].Format("2006-01-02") != "2026-12-30" || days[5].Format("2006-01-02") != "2027-01-04" {
		t.Fatalf("days = %v", days)
	}
	if got, want := fmt.Sprint(weeks), "[2026-W53 2027-W01]"; got != want {
		t.Fatalf("weeks = %s, want %s", got, want)
	}
	if got, want := fmt.Sprint(months), "[2026-12 2027-01]"; got != want {
		t.Fatalf("months = %s, want %s", got, want)
	}
}

func TestBackfillUnits_RejectsInvertedRange(t *testing.T) {
	if _, _, _, err := backfillUnits("2026-10-02", "2026-10-01"); err == nil {
		t.Fatal("expected an error for --to before --from")
	}
}
