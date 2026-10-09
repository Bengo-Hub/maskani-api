package billing

import (
	"testing"
	"time"
)

// A billing day after the reading window bills the same month; an earlier one bills the month
// whose readings were taken at the end of the month before.
func TestScheduleTargets(t *testing.T) {
	loc := time.FixedZone("EAT", 3*3600)
	now := time.Date(2026, 10, 10, 9, 0, 0, 0, loc)

	got := targets(now, loc, 1, 28)
	if got[0].period != "2026-09" || got[0].date.Day() != 1 || got[0].date.Month() != time.October {
		t.Fatalf("billing day 1, this month: %+v", got[0])
	}
	if got[1].period != "2026-10" || got[1].date.Month() != time.November {
		t.Fatalf("billing day 1, next month: %+v", got[1])
	}

	got = targets(now, loc, 28, 26)
	if got[0].period != "2026-10" || got[0].date.Day() != 28 || got[0].date.Month() != time.October {
		t.Fatalf("billing day 28 after a window ending on the 26th: %+v", got[0])
	}
}

func TestScheduleDefaultsAndTrim(t *testing.T) {
	sc := Schedule{RemindDaysBefore: 50}
	sc.defaults()
	if sc.Fund != "estate" || sc.Mode != "auto" || sc.MissingReadings != "wait" || sc.RemindDaysBefore != 20 {
		t.Fatalf("defaults: %+v", sc)
	}
	sc.Sent = map[string]string{"2026-01:reminder": "x", "2026-10:waiting": "y"}
	sc.Approved = map[string]string{"2025-12": "a", "2026-09": "b"}
	sc.trim("2026-10")
	if _, ok := sc.Sent["2026-01:reminder"]; ok || sc.Sent["2026-10:waiting"] == "" {
		t.Fatalf("sent after trim: %v", sc.Sent)
	}
	if _, ok := sc.Approved["2025-12"]; ok || sc.Approved["2026-09"] == "" {
		t.Fatalf("approved after trim: %v", sc.Approved)
	}
}
