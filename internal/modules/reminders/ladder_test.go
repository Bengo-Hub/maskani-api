package reminders

import (
	"testing"

	"github.com/bengobox/maskani-api/internal/modules/settings"
)

func TestLadderNext(t *testing.T) {
	steps := settings.DefaultArrearsSteps
	// Day 1: the first reminder.
	st, done, ok := next(steps, Ladder{}, 1, "2026-10-10")
	if !ok || st.Day != 1 || len(done) != 1 {
		t.Fatalf("day 1: %+v %v %v", st, done, ok)
	}
	// A debt first seen at 20 days sends only the day 14 reminder and marks 1, 7 and 14 done.
	st, done, ok = next(steps, Ladder{}, 20, "2026-10-10")
	if !ok || st.Day != 14 || len(done) != 3 {
		t.Fatalf("catch-up: %+v %v %v", st, done, ok)
	}
	// Nothing new on day 20 once 14 is done.
	if _, _, ok = next(steps, Ladder{Done: []int{1, 7, 14}}, 20, "2026-10-10"); ok {
		t.Fatal("no step expected between 14 and 30")
	}
	// A promise to pay holds back the demand letter, not the call list.
	l := Ladder{Done: []int{1, 7, 14}, PromiseDate: "2026-10-20"}
	st, _, ok = next(steps, l, 50, "2026-10-10")
	if !ok || st.Action != settings.ArrearsCallList {
		t.Fatalf("promise held: %+v %v", st, ok)
	}
	// After the promised date the letter goes.
	l.Done = append(l.Done, 30)
	st, _, ok = next(steps, l, 50, "2026-10-21")
	if !ok || st.Action != settings.ArrearsDemandLetter {
		t.Fatalf("promise passed: %+v %v", st, ok)
	}
}
