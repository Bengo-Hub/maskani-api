package collections

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/bengobox/maskani-api/internal/modules/treasury"
)

// A bill of 10,000 with 2,000 credited and 3,000 paid leaves 5,000; each entry's balance follows.
func TestEntriesShowCredits(t *testing.T) {
	d := func(n int64) decimal.Decimal { return decimal.NewFromInt(n) }
	day := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	led := &treasury.AccountLedger{Balance: d(5000),
		Invoices: []treasury.LedgerInvoice{{ID: uuid.New(), InvoiceNumber: "INV-1", InvoiceDate: day, TotalAmount: d(10000), AmountPaid: d(3000), AmountCredited: d(2000)}},
		Payments: []treasury.LedgerPayment{{ID: uuid.New(), Amount: d(3000), PaidAt: day.AddDate(0, 0, 3)}},
	}
	got, _ := Entries(led, 50)
	want := []struct {
		kind    string
		balance int64
	}{{"payment", 5000}, {"credit", 8000}, {"bill", 10000}}
	if len(got) != len(want) {
		t.Fatalf("entries: %+v", got)
	}
	for i, w := range want {
		if got[i].Kind != w.kind || !got[i].BalanceAfter.Equal(d(w.balance)) {
			t.Fatalf("entry %d: %s %s, want %s %d", i, got[i].Kind, got[i].BalanceAfter, w.kind, w.balance)
		}
	}
}
