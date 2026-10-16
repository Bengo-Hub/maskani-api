package works

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/config"
	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/outboxevent"
	_ "github.com/bengobox/maskani-api/internal/ent/runtime"
	"github.com/bengobox/maskani-api/internal/ent/servicevisit"
	"github.com/bengobox/maskani-api/internal/ent/vendorinvoice"
	"github.com/bengobox/maskani-api/internal/modules/approvals"
	"github.com/bengobox/maskani-api/internal/platform/database"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// TestVendorInvoicesOnPostgres: an invoice needs the vendor linked to treasury, is recorded as
// issued and waits for approval; the check sets it beside the contract fee and the visits (two
// done, one missed at 5 percent); the person who entered it cannot approve it, a rejection needs a
// note; approval publishes the treasury event with the invoiced total; the same number again is
// refused; cancelling an approved invoice tells treasury to void its bill. Needs
// WORKS_TEST_POSTGRES_URL (a scratch database).
func TestVendorInvoicesOnPostgres(t *testing.T) {
	dsn := os.Getenv("WORKS_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("WORKS_TEST_POSTGRES_URL not set")
	}
	db, err := database.OpenSQL(dsn, config.PostgresConfig{MaxOpenConns: 2, MaxIdleConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer client.Close()
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	tenant := uuid.New()
	ctx := tenantguard.With(context.Background(), tenant)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	s := NewService(client, nil, zap.NewNop())
	s.SetApprovals(approvals.NewService(client))
	prop, err := client.Property.Create().SetCode("MV").SetName("Mvumo").Save(ctx)
	must(err)
	v, err := client.Vendor.Create().SetName("CleanCo").Save(ctx)
	must(err)
	oct := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	c, err := s.CreateContract(ctx, v.ID, ContractInput{PropertyID: prop.ID, ServiceCategory: "cleaning", FeeBasis: "fixed_monthly",
		FeeAmount: decimal.NewFromInt(10000), StartsOn: oct.AddDate(0, -1, 0), ServiceCredits: []map[string]any{{"breach": "missed_visit", "percent": 5.0}}})
	must(err)
	for i, st := range []servicevisit.Status{servicevisit.StatusCompleted, servicevisit.StatusCompleted, servicevisit.StatusMissed} {
		_, err := client.ServiceVisit.Create().SetContractID(c.ID).SetVendorID(v.ID).SetPropertyID(prop.ID).
			SetDueAt(oct.AddDate(0, 0, i*7)).SetStatus(st).Save(ctx)
		must(err)
	}

	clerk := uuid.New()
	in := InvoiceInput{ContractID: c.ID, InvoiceNumber: "INV-10", EtimsNumber: "KRACU01", InvoiceDate: oct.AddDate(0, 1, 0),
		PeriodStart: oct, PeriodEnd: oct.AddDate(0, 1, -1), Amount: decimal.RequireFromString("11020"), VATAmount: decimal.RequireFromString("1520")}
	if _, err := s.CreateInvoice(ctx, in, clerk, "Clerk"); err == nil {
		t.Fatal("a vendor not linked to treasury cannot be invoiced")
	}
	tv := uuid.New()
	must(v.Update().SetTreasuryVendorID(tv).Exec(ctx))
	bad := in
	bad.VATAmount = decimal.RequireFromString("11020")
	if _, err := s.CreateInvoice(ctx, bad, clerk, "Clerk"); err == nil {
		t.Fatal("VAT must be less than the total")
	}
	inv, err := s.CreateInvoice(ctx, in, clerk, "Clerk")
	must(err)
	if inv.Status != vendorinvoice.StatusSubmitted {
		t.Fatalf("waits for approval: %s", inv.Status)
	}
	if _, err := s.CreateInvoice(ctx, in, clerk, "Clerk"); err == nil {
		t.Fatal("the same invoice number twice is refused")
	}

	chk, err := s.CheckInvoice(ctx, inv.ID)
	must(err)
	if chk.Visits["completed"] != 2 || chk.Visits["missed"] != 1 || !chk.CreditDue.Equal(decimal.NewFromInt(500)) ||
		!chk.ExpectedAmount.Equal(decimal.NewFromInt(9500)) || !chk.Difference.Equal(decimal.NewFromInt(1520)) || chk.Months != 1 {
		t.Fatalf("check: %+v", chk)
	}

	anyPerm := func(string) bool { return true }
	if _, err := s.DecideInvoice(ctx, inv.ID, approvals.Actor{UserID: clerk, HasPerm: anyPerm}, approvals.Approve, ""); err == nil {
		t.Fatal("the person who entered it cannot approve it")
	}
	manager := approvals.Actor{UserID: uuid.New(), Name: "Manager", HasPerm: anyPerm}
	if _, err := s.DecideInvoice(ctx, inv.ID, manager, approvals.Reject, ""); err == nil {
		t.Fatal("a rejection needs a note")
	}
	got, err := s.DecideInvoice(ctx, inv.ID, manager, approvals.Approve, "")
	must(err)
	if got.Status != vendorinvoice.StatusApproved || got.DecidedAt == nil {
		t.Fatalf("approved: %s", got.Status)
	}
	ev, err := client.OutboxEvent.Query().Where(outboxevent.EventType(VendorInvoiceApproved)).Only(ctx)
	must(err)
	var env struct {
		Payload map[string]any `json:"payload"`
	}
	must(json.Unmarshal(ev.Payload, &env))
	for k, want := range map[string]string{"amount": "11020.00", "vat_amount": "1520.00", "treasury_vendor_id": tv.String(),
		"etims_number": "KRACU01", "invoice_id": inv.ID.String(), "invoice_number": "INV-10"} {
		if env.Payload[k] != want {
			t.Fatalf("approved event %s = %v, want %s", k, env.Payload[k], want)
		}
	}

	if _, err := s.CancelInvoice(ctx, inv.ID, manager.UserID, ""); err == nil {
		t.Fatal("a cancel needs a reason")
	}
	cancelled, err := s.CancelInvoice(ctx, inv.ID, manager.UserID, "wrong period")
	must(err)
	if cancelled.Status != vendorinvoice.StatusCancelled {
		t.Fatalf("cancelled: %s", cancelled.Status)
	}
	if n, _ := client.OutboxEvent.Query().Where(outboxevent.EventType(VendorInvoiceCancelled)).Count(ctx); n != 1 {
		t.Fatalf("cancel event: %d", n)
	}
	if n, err := s.SyncInvoicePayments(tenantguard.System(ctx), []uuid.UUID{tenant}); err != nil || n != 0 {
		t.Fatalf("no treasury, nothing synced: %d %v", n, err)
	}
}
