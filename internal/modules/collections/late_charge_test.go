package collections

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/bengobox/maskani-api/internal/modules/settings"
	"github.com/bengobox/maskani-api/internal/modules/treasury"
)

// Only bills past due plus grace count, net of payments and credits, and never an earlier late charge.
func TestLateChargeNeverCompounds(t *testing.T) {
	d := func(n int64) decimal.Decimal { return decimal.NewFromInt(n) }
	today := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	led := &treasury.AccountLedger{Invoices: []treasury.LedgerInvoice{
		{DueDate: today.AddDate(0, 0, -45), TotalAmount: d(10000), AmountPaid: d(2000), AmountCredited: d(1000)}, // 7,000 overdue
		{DueDate: today.AddDate(0, 0, -45), TotalAmount: d(140), Kind: KindLateCharge},                            // last month's charge
		{DueDate: today.AddDate(0, 0, -10), TotalAmount: d(5000)},                                                 // inside grace
		{DueDate: today.AddDate(0, 0, -60), TotalAmount: d(3000), AmountPaid: d(3000)},                           // paid
	}}
	base := OverdueBase(led, today, 30)
	if !base.Equal(d(7000)) {
		t.Fatalf("base %s, want 7000", base)
	}
	lc := settings.LateCharge{Percent: d(2), Fixed: d(100), Cap: d(500)}
	if got := lc.Amount(base); !got.Equal(d(240)) {
		t.Fatalf("2%% of 7000 plus 100 = %s, want 240", got)
	}
	if got := lc.Amount(d(100000)); !got.Equal(d(500)) {
		t.Fatalf("capped: %s, want 500", got)
	}
	if got := lc.Amount(decimal.Zero); !got.IsZero() {
		t.Fatalf("nothing overdue: %s", got)
	}
}
