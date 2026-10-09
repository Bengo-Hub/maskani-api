package collections

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/bengobox/maskani-api/internal/modules/treasury"
)

func day(d int) time.Time { return time.Date(2026, 9, d, 0, 0, 0, 0, time.UTC) }

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestEntriesBalanceAfterEach(t *testing.T) {
	led := &treasury.AccountLedger{
		Balance: dec("1500"), Credit: dec("0"),
		Invoices: []treasury.LedgerInvoice{
			{InvoiceNumber: "INV-1", InvoiceDate: day(1), TotalAmount: dec("3000")},
			{InvoiceNumber: "INV-2", InvoiceDate: day(20), TotalAmount: dec("1500")},
		},
		Payments: []treasury.LedgerPayment{{Amount: dec("3000"), Method: "mpesa_c2b", PaidAt: day(20)}},
	}
	got, trimmed := Entries(led, 50)
	if trimmed || len(got) != 3 {
		t.Fatalf("got %d entries, trimmed %v", len(got), trimmed)
	}
	// Same day: the payment sits above the bill. Balances run back from 1500.
	want := []struct {
		kind, after string
	}{{"payment", "1500"}, {"bill", "4500"}, {"bill", "3000"}}
	for i, w := range want {
		if got[i].Kind != w.kind || !got[i].BalanceAfter.Equal(dec(w.after)) {
			t.Fatalf("entry %d = %s after %s, want %s after %s", i, got[i].Kind, got[i].BalanceAfter, w.kind, w.after)
		}
	}
	if got[0].Label != "Payment, mpesa c2b" {
		t.Fatalf("payment label = %q", got[0].Label)
	}
}

// A full list means older entries of the other kind may be missing, so entries older than the
// full list's oldest are dropped and the statement says it is trimmed.
func TestEntriesTrimsAtFullList(t *testing.T) {
	led := &treasury.AccountLedger{
		Balance: dec("0"),
		Invoices: []treasury.LedgerInvoice{
			{InvoiceNumber: "INV-3", InvoiceDate: day(25), TotalAmount: dec("100")},
			{InvoiceNumber: "INV-2", InvoiceDate: day(15), TotalAmount: dec("100")},
		},
		Payments: []treasury.LedgerPayment{{Amount: dec("100"), PaidAt: day(26)}, {Amount: dec("100"), PaidAt: day(5)}},
	}
	got, trimmed := Entries(led, 2)
	if !trimmed {
		t.Fatal("expected trimmed")
	}
	for _, e := range got {
		if e.Date.Before(day(15)) {
			t.Fatalf("kept an entry from %s, older than the cutoff", e.Date)
		}
	}
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3", len(got))
	}
}

func TestEntriesCreditMakesNegativeBalance(t *testing.T) {
	led := &treasury.AccountLedger{Balance: dec("0"), Credit: dec("200"),
		Payments: []treasury.LedgerPayment{{Amount: dec("200"), PaidAt: day(2)}}}
	got, _ := Entries(led, 50)
	if !got[0].BalanceAfter.Equal(dec("-200")) {
		t.Fatalf("balance after = %s, want -200 (in credit)", got[0].BalanceAfter)
	}
}
