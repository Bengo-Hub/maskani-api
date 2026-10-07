// Package sales covers price lists, reservations, sale contracts, instalment schedules and their
// invoicing through treasury (SRDD section 9).
package sales

import (
	"time"

	"github.com/shopspring/decimal"
)

// ScheduleLine is one generated instalment.
type ScheduleLine struct {
	Seq     int             `json:"seq"`
	Kind    string          `json:"kind"`
	DueDate time.Time       `json:"due_date"`
	Amount  decimal.Decimal `json:"amount"`
	Label   string          `json:"label,omitempty"`
}

// ScheduleInput is what the schedule depends on.
type ScheduleInput struct {
	NetPrice          decimal.Decimal
	Deposit           decimal.Decimal // includes the reservation credit
	ReservationCredit decimal.Decimal
	Option            string // outright, instalments, milestone, financed
	Frequency         string // monthly, quarterly, milestone, once
	TermMonths        int
	Start             time.Time // agreement date
	DepositDue        time.Time
	FirstDue          time.Time
	Milestones        []Milestone
	FinancierAmount   decimal.Decimal
}

// Milestone is a construction milestone share for milestone schedules.
type Milestone struct {
	Label string  `json:"label"`
	Pct   float64 `json:"pct"`
}

// BuildSchedule generates the payment schedule. The deposit line is net of the reservation fee
// already paid, the balance is split evenly and the last instalment absorbs rounding, so the
// schedule always sums to the net price less the reservation credit (SRDD 9.1 to 9.3).
func BuildSchedule(in ScheduleInput) []ScheduleLine {
	var out []ScheduleLine
	seq := 1
	add := func(kind string, due time.Time, amt decimal.Decimal, label string) {
		if amt.IsPositive() {
			out = append(out, ScheduleLine{Seq: seq, Kind: kind, DueDate: due, Amount: amt.Round(2), Label: label})
			seq++
		}
	}
	payable := in.NetPrice.Sub(in.ReservationCredit)
	if in.Option == "outright" {
		add("balance", in.DepositDue, payable, "Balance of purchase price")
		return out
	}
	depositNet := in.Deposit.Sub(in.ReservationCredit)
	if depositNet.IsNegative() {
		depositNet = decimal.Zero
	}
	add("deposit", in.DepositDue, depositNet, "Deposit")
	balance := payable.Sub(depositNet)

	if in.Option == "financed" && in.FinancierAmount.IsPositive() {
		own := balance.Sub(in.FinancierAmount)
		add("instalment", in.FirstDue, own, "Buyer contribution")
		add("financier", in.FirstDue, in.FinancierAmount, "Financier release")
		return out
	}

	if in.Option == "milestone" || in.Frequency == "milestone" {
		remaining := balance
		for i, m := range in.Milestones {
			amt := balance.Mul(decimal.NewFromFloat(m.Pct)).Div(decimal.NewFromInt(100)).Round(2)
			if i == len(in.Milestones)-1 {
				amt = remaining
			}
			remaining = remaining.Sub(amt)
			// Indicative date only: a milestone falls due when the sales officer releases it.
			add("milestone", in.DepositDue.AddDate(0, 6*(i+1), 0), amt, m.Label)
		}
		return out
	}

	step := 1
	if in.Frequency == "quarterly" {
		step = 3
	}
	n := in.TermMonths / step
	if n < 1 {
		n = 1
	}
	each := balance.Div(decimal.NewFromInt(int64(n))).RoundDown(2)
	remaining := balance
	for i := 0; i < n; i++ {
		amt := each
		if i == n-1 {
			amt = remaining
		}
		remaining = remaining.Sub(amt)
		add("instalment", in.FirstDue.AddDate(0, i*step, 0), amt, "")
	}
	return out
}
