package sales

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func sum(lines []ScheduleLine) decimal.Decimal {
	t := decimal.Zero
	for _, l := range lines {
		t = t.Add(l.Amount)
	}
	return t
}

// SRDD 9.2: 7,500,000, reservation 100,000 credited, deposit 20% = 1,500,000, 24 x 250,000.
func TestWorkedExample(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	lines := BuildSchedule(ScheduleInput{
		NetPrice: decimal.NewFromInt(7_500_000), Deposit: decimal.NewFromInt(1_500_000),
		ReservationCredit: decimal.NewFromInt(100_000), Option: "instalments", Frequency: "monthly",
		TermMonths: 24, Start: start, DepositDue: start, FirstDue: start.AddDate(0, 1, 0),
	})
	if len(lines) != 25 {
		t.Fatalf("lines = %d, want 25", len(lines))
	}
	if !lines[0].Amount.Equal(decimal.NewFromInt(1_400_000)) {
		t.Fatalf("deposit net of reservation = %s", lines[0].Amount)
	}
	for _, l := range lines[1:] {
		if !l.Amount.Equal(decimal.NewFromInt(250_000)) {
			t.Fatalf("instalment = %s", l.Amount)
		}
	}
	if !sum(lines).Equal(decimal.NewFromInt(7_400_000)) {
		t.Fatalf("schedule sum = %s, want price less reservation credit", sum(lines))
	}
}

func TestLastInstalmentAbsorbsRounding(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	lines := BuildSchedule(ScheduleInput{NetPrice: decimal.NewFromInt(1_000_000), Deposit: decimal.Zero,
		Option: "instalments", Frequency: "monthly", TermMonths: 3, DepositDue: start, FirstDue: start})
	if !sum(lines).Equal(decimal.NewFromInt(1_000_000)) {
		t.Fatalf("sum = %s", sum(lines))
	}
	if !lines[2].Amount.Equal(decimal.RequireFromString("333333.34")) {
		t.Fatalf("last = %s", lines[2].Amount)
	}
}

func TestOutrightAndMilestones(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	out := BuildSchedule(ScheduleInput{NetPrice: decimal.NewFromInt(5_000_000), ReservationCredit: decimal.NewFromInt(100_000),
		Option: "outright", DepositDue: start})
	if len(out) != 1 || !out[0].Amount.Equal(decimal.NewFromInt(4_900_000)) {
		t.Fatalf("outright = %+v", out)
	}
	ms := BuildSchedule(ScheduleInput{NetPrice: decimal.NewFromInt(6_000_000), Deposit: decimal.NewFromInt(1_000_000),
		Option: "milestone", DepositDue: start, Milestones: []Milestone{{"Foundation", 30}, {"Roofing", 40}, {"Completion", 30}}})
	if !sum(ms).Equal(decimal.NewFromInt(6_000_000)) || len(ms) != 4 {
		t.Fatalf("milestones = %+v", ms)
	}
}
