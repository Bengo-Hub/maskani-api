package collections

import (
	"context"
	"os"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/config"
	"github.com/bengobox/maskani-api/internal/ent"
	_ "github.com/bengobox/maskani-api/internal/ent/runtime"
	"github.com/bengobox/maskani-api/internal/modules/approvals"
	"github.com/bengobox/maskani-api/internal/platform/database"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
	"github.com/bengobox/maskani-api/internal/shared/page"
)

// TestManualPaymentsOnPostgres covers the review rules that run before treasury is called: one
// slip once, residents limited to bank and cheque, no self-approval, and a rejected slip that can
// be corrected and submitted again. Needs COLLECTIONS_TEST_POSTGRES_URL (a scratch database).
func TestManualPaymentsOnPostgres(t *testing.T) {
	dsn := os.Getenv("COLLECTIONS_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("COLLECTIONS_TEST_POSTGRES_URL not set")
	}
	db, err := database.OpenSQL(dsn, config.PostgresConfig{MaxOpenConns: 2, MaxIdleConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer client.Close()
	ctx := context.Background()
	if err := client.Schema.Create(ctx); err != nil {
		t.Fatal(err)
	}
	tctx := tenantguard.With(ctx, uuid.New())
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	p, err := client.Property.Create().SetCode("SV").SetName("Shaba").Save(tctx)
	must(err)
	f, err := client.Fund.Create().SetCode("estate").SetName("Estate").Save(tctx)
	must(err)
	u, err := client.Unit.Create().SetPropertyID(p.ID).SetCode("A01").Save(tctx)
	must(err)
	acc, err := client.UnitAccount.Create().SetUnitID(u.ID).SetFundID(f.ID).SetAccountRef("A01").Save(tctx)
	must(err)
	ap := approvals.NewService(client)
	s := NewService(client, nil, nil, ap, zap.NewNop())
	anyone := func(string) bool { return true }
	clerk := Submitter{UserID: uuid.New(), Name: "Clerk"}
	in := ManualInput{Amount: decimal.NewFromInt(300000), Method: "bank_transfer", Reference: "ft-001"}

	mp, err := s.SubmitManual(tctx, acc.ID, clerk, in)
	must(err)
	if mp.Reference != "FT-001" || mp.Status != "pending" {
		t.Fatalf("submitted: %+v", mp)
	}
	if _, err := s.SubmitManual(tctx, acc.ID, clerk, in); err == nil {
		t.Fatal("the same slip twice should be refused")
	}
	if _, err := s.SubmitManual(tctx, acc.ID, Submitter{UserID: uuid.New(), Portal: true}, ManualInput{Amount: decimal.NewFromInt(10), Method: "cash", Reference: "X1"}); err == nil {
		t.Fatal("a resident should not record cash")
	}
	if _, err := s.ApproveManual(tctx, mp.ID, approvals.Actor{UserID: clerk.UserID, HasPerm: anyone}, ""); err == nil {
		t.Fatal("the person who recorded it should not approve it")
	}
	if _, err := s.RejectManual(tctx, mp.ID, approvals.Actor{UserID: uuid.New(), Name: "Manager", HasPerm: anyone}, "slip unreadable"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitManual(tctx, acc.ID, clerk, in); err != nil {
		t.Fatalf("a rejected slip should be submittable again: %v", err)
	}

	// Bank statement import: Lockwood-style "account#ref", a phone, a repeat, and an unknown line.
	u2, err := client.Unit.Create().SetPropertyID(p.ID).SetCode("TAN7").Save(tctx)
	must(err)
	tan, err := client.UnitAccount.Create().SetUnitID(u2.ID).SetFundID(f.ID).SetAccountRef("TAN7").SetCustomerPhone("254712345678").Save(tctx)
	must(err)
	lines := []BankLine{
		{Date: "2026-10-01", Amount: decimal.NewFromInt(17400), Reference: "FT26274A", Description: "MPESA C2B 2362010#TAN7 TITUS"},
		{Date: "2026-10-02", Amount: decimal.NewFromInt(500), Reference: "FT26275B", Description: "Deposit from 0712345678"},
		{Date: "2026-10-01", Amount: decimal.NewFromInt(17400), Reference: "FT26274A", Description: "MPESA C2B 2362010#TAN7 TITUS"},
		{Date: "2026-10-03", Amount: decimal.NewFromInt(900), Reference: "FT26276C", Description: "Unknown payer"},
	}
	res, err := s.ImportBankLines(tctx, "estate", nil, true, clerk, lines)
	must(err)
	want := []string{"queued", "queued", "duplicate", "unmatched"}
	for i, r := range res {
		if r.Status != want[i] {
			t.Fatalf("line %d: %s (%s), want %s", i, r.Status, r.Error, want[i])
		}
		if r.Status == "queued" && (r.AccountID == nil || *r.AccountID != tan.ID) {
			t.Fatalf("line %d matched the wrong account: %+v", i, r)
		}
	}
	if res[0].MatchedBy != "reference" || res[1].MatchedBy != "phone" {
		t.Fatalf("matched by: %s, %s", res[0].MatchedBy, res[1].MatchedBy)
	}

	// Bill queries: a resident raises one, finance takes it, then answers; a second answer is refused.
	bq, err := s.RaiseBillQuery(tctx, acc.ID, Raiser{UserID: uuid.New(), Name: "Owner"}, BillQueryInput{Subject: "Water charge", Body: "The reading looks doubled"})
	must(err)
	if bq.Status != "open" || bq.DueBy == nil || bq.PropertyID != p.ID {
		t.Fatalf("raised: %+v", bq)
	}
	if _, err := s.AnswerBillQuery(tctx, bq.ID, Reviewer{UserID: uuid.New(), Name: "Finance"}, BillQueryAnswer{Status: "resolved"}); err == nil {
		t.Fatal("resolving without an answer should be refused")
	}
	if _, err := s.AnswerBillQuery(tctx, bq.ID, Reviewer{UserID: uuid.New(), Name: "Finance"}, BillQueryAnswer{Status: "in_review"}); err != nil {
		t.Fatal(err)
	}
	done, err := s.AnswerBillQuery(tctx, bq.ID, Reviewer{UserID: uuid.New(), Name: "Finance"}, BillQueryAnswer{Status: "resolved", Resolution: "Meter re-read; credit raised"})
	must(err)
	if done.Status != "resolved" || done.Metadata["answered_by_name"] != "Finance" {
		t.Fatalf("answered: %+v", done)
	}
	if _, err := s.AnswerBillQuery(tctx, bq.ID, Reviewer{UserID: uuid.New()}, BillQueryAnswer{Status: "rejected", Resolution: "x"}); err == nil {
		t.Fatal("an answered query should not be answered again")
	}

	// The recorder cannot verify; a manual payment without the permission cannot be verified either.
	mp2, err := s.SubmitManual(tctx, acc.ID, clerk, ManualInput{Amount: decimal.NewFromInt(5000), Method: "cheque", Reference: "CHQ-9"})
	must(err)
	if _, err := s.ApproveManual(tctx, mp2.ID, approvals.Actor{UserID: uuid.New(), HasPerm: func(string) bool { return false }}, ""); err == nil {
		t.Fatal("someone without billing.verify should not verify")
	}
	if req, _ := ap.Latest(tctx, mp2.ID); req == nil || req.CurrentApprover != "perm:maskani.billing.verify" {
		t.Fatalf("manual payment request: %+v", req)
	}

	// Adjustments: a two-step rule above 10,000 (tenant admin, then finance officer); the requester
	// cannot approve, a role the step does not name cannot, one person approves one step only.
	_, err = ap.CreateRule(tctx, approvals.RuleInput{Module: "credit_note", Name: "Large credits", MinAmount: decimal.NewFromInt(10000),
		Steps: []approvals.Step{{ApproverRole: "tenant_admin"}, {ApproverRole: "finance_officer"}}})
	must(err)
	asker := uuid.New()
	adj, err := client.Adjustment.Create().SetUnitAccountID(acc.ID).SetPropertyID(p.ID).SetKind("credit_note").
		SetAmount(decimal.NewFromInt(12000)).SetReason("double billed").SetRequestedBy(asker).Save(tctx)
	must(err)
	pid := p.ID
	_, required, err := ap.Submit(tctx, approvals.Submission{Module: "credit_note", ObjectID: adj.ID, Amount: adj.Amount, PropertyID: &pid, By: asker})
	must(err)
	if !required {
		t.Fatal("a rule matched, so approval is required")
	}
	if _, err := s.ApproveAdjustment(tctx, adj.ID, approvals.Actor{UserID: asker, Roles: []string{"tenant_admin"}}, ""); err == nil {
		t.Fatal("the requester should not approve their own credit")
	}
	if _, err := s.ApproveAdjustment(tctx, adj.ID, approvals.Actor{UserID: uuid.New(), Roles: []string{"finance_officer"}}, ""); err == nil {
		t.Fatal("step one names the tenant admin, not finance")
	}
	first := uuid.New()
	one, err := s.ApproveAdjustment(tctx, adj.ID, approvals.Actor{UserID: first, Name: "A", Roles: []string{"tenant_admin", "finance_officer"}}, "")
	must(err)
	req, _ := ap.Latest(tctx, adj.ID)
	if one.Status != "pending_approval" || req.CurrentApprover != "finance_officer" {
		t.Fatalf("after step one: %s, next %s", one.Status, req.CurrentApprover)
	}
	if _, err := s.ApproveAdjustment(tctx, adj.ID, approvals.Actor{UserID: first, Roles: []string{"finance_officer"}}, ""); err == nil {
		t.Fatal("one person should not approve two steps")
	}
	inbox, err := ap.List(tctx, approvals.Filter{Status: "pending", All: true, Approvers: []string{"finance_officer"}}, page.Params{Limit: 10})
	must(err)
	if len(inbox.Data) != 1 || inbox.Data[0].ObjectID != adj.ID {
		t.Fatalf("finance inbox: %d", len(inbox.Data))
	}
	if _, err := s.RejectAdjustment(tctx, adj.ID, approvals.Actor{UserID: uuid.New(), Name: "B", Roles: []string{"finance_officer"}}, "not a billing error"); err != nil {
		t.Fatal(err)
	}
	if got, _ := client.Adjustment.Get(tctx, adj.ID); got.Status != "rejected" || got.Metadata["rejected_reason"] != "not a billing error" {
		t.Fatalf("rejected: %+v", got)
	}
	if _, err := ap.CreateRule(tctx, approvals.RuleInput{Module: "credit_note", MinAmount: decimal.NewFromInt(50000), Steps: []approvals.Step{{ApproverRole: "x"}}}); err == nil {
		t.Fatal("an overlapping band should be refused")
	}
}
