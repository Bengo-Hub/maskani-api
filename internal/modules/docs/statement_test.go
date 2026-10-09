package docs

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/Bengo-Hub/reports"
	"github.com/shopspring/decimal"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/modules/collections"
	"github.com/bengobox/maskani-api/internal/modules/treasury"
)

func sampleStatement() *collections.Statement {
	d := func(s string) decimal.Decimal { return decimal.RequireFromString(s) }
	day := func(m time.Month, dd int) time.Time { return time.Date(2026, m, dd, 9, 0, 0, 0, time.UTC) }
	led := &treasury.AccountLedger{Balance: d("6500"), TotalBilled: d("19500"), TotalPaid: d("13000"),
		Invoices: []treasury.LedgerInvoice{
			{InvoiceNumber: "SV-INV-0101", InvoiceDate: day(7, 1), TotalAmount: d("6500"), PaymentStatus: "paid", Description: "Service charge, July"},
			{InvoiceNumber: "SV-INV-0188", InvoiceDate: day(8, 1), TotalAmount: d("6500"), PaymentStatus: "paid", Description: "Service charge, August"},
			{InvoiceNumber: "SV-INV-0275", InvoiceDate: day(9, 1), TotalAmount: d("6500"), PaymentStatus: "unpaid", Description: "Service charge, September — Café block"},
		},
		Payments: []treasury.LedgerPayment{
			{Amount: d("6500"), Method: "mpesa_c2b", Reference: "SGH12KX9", PaidAt: day(7, 4)},
			{Amount: d("6500"), Method: "mpesa_c2b", Reference: "SHK33LQ2", PaidAt: day(8, 6)},
		}}
	entries, trimmed := collections.Entries(led, 50)
	acc := &ent.UnitAccount{AccountRef: "SV-A12", CustomerName: "Wanjiru Kamau"}
	acc.Edges.Unit = &ent.Unit{Code: "A12"}
	acc.Edges.Fund = &ent.Fund{Name: "Estate fund", PaybillShortcode: "4123456"}
	return &collections.Statement{Account: acc, Ledger: led, Entries: entries, Trimmed: trimmed}
}

// TestStatementReport renders the statement in every format; with $STATEMENT_SAMPLE_PDF set it
// also writes the PDF to look at.
func TestStatementReport(t *testing.T) {
	r := &reports.Report{GeneratedAt: time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC), Currency: "KES",
		TenantName: "Shaba Village Estate", OutletName: "Shaba Village Phase 1", PrimaryColor: "#8E2C76"}
	statementReport(r, sampleStatement(), time.UTC)

	if got := r.Sections[0].Rows; len(got) != 5 || got[0][1].Text != "Service charge, July" {
		t.Fatalf("rows not oldest first: %+v", got)
	}
	if last := r.Sections[0].Rows[4][5].Text; last != "KES 6,500.00" {
		t.Fatalf("balance after the last entry = %q, want KES 6,500.00", last)
	}
	for _, f := range []reports.Format{reports.FormatPDF, reports.FormatCSV, reports.FormatXLSX} {
		b, _, err := reports.Generate(r, f)
		if err != nil || len(b) == 0 {
			t.Fatalf("%s: %v", f, err)
		}
		if f == reports.FormatCSV && !bytes.Contains(b, []byte("SV-INV-0275")) {
			t.Fatal("CSV is missing a bill")
		}
		if out := os.Getenv("STATEMENT_SAMPLE_PDF"); out != "" && f == reports.FormatPDF {
			if err := os.WriteFile(out, b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestAddressAndFileName(t *testing.T) {
	got := address(map[string]any{"address": "Kiambu Road", "postal_code": "00100", "city": "Nairobi", "country_name": "Kenya"}, "KE")
	if got != "Kiambu Road\n00100 Nairobi\nKenya" {
		t.Fatalf("address = %q", got)
	}
	if got := address(nil, "KE"); got != "KE" {
		t.Fatalf("empty address = %q, want the country", got)
	}
	if got := fileSafe("SV/A 12"); got != "SV-A-12" {
		t.Fatalf("fileSafe = %q", got)
	}
}
