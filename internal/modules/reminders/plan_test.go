package reminders

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestPlanState(t *testing.T) {
	d := func(n int64) decimal.Decimal { return decimal.NewFromInt(n) }
	p := Plan{Total: d(30000), Instalments: []PlanInstalment{{Due: "2026-10-05", Amount: d(10000)}, {Due: "2026-11-05", Amount: d(10000)}, {Due: "2026-12-05", Amount: d(10000)}}}
	day := func(s string) time.Time { t, _ := time.Parse("2006-01-02", s); return t }
	cases := []struct {
		paid  int64
		today string
		want  string
	}{
		{0, "2026-10-07", PlanActive},     // inside the three grace days
		{0, "2026-10-09", PlanBroken},     // first instalment missed
		{10000, "2026-11-07", PlanActive}, // second still in grace
		{10000, "2026-11-09", PlanBroken}, // second missed
		{20000, "2026-11-20", PlanActive},
		{30000, "2026-11-20", PlanCompleted}, // paid early
	}
	for _, c := range cases {
		if got := planState(p, d(c.paid), day(c.today)); got != c.want {
			t.Errorf("paid %d on %s: %s, want %s", c.paid, c.today, got, c.want)
		}
	}
	l := Ladder{Plan: &Plan{Status: PlanActive}}
	if _, _, ok := next(nil, l, 50, "2026-10-10"); ok {
		t.Fatal("no steps, nothing to run")
	}
}
