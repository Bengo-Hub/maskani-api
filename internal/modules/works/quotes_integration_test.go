package works

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
)

// TestQuoteApprovalOnPostgres: a quote opens an approval on the central engine; the person who
// entered it cannot approve it; a two-step rule for large quotes needs both steps; a rejection sends
// the work order back to the assignee. Needs WORKS_TEST_POSTGRES_URL (a scratch database).
func TestQuoteApprovalOnPostgres(t *testing.T) {
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
	ctx := tenantguard.With(context.Background(), uuid.New())
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	ap := approvals.NewService(client)
	s := NewService(client, nil, zap.NewNop())
	s.SetApprovals(ap)
	p, err := client.Property.Create().SetCode("SV").SetName("Shaba").Save(ctx)
	must(err)
	newWO := func(n string) *ent.WorkOrder {
		wo, err := client.WorkOrder.Create().SetNumber(n).SetPropertyID(p.ID).SetCategory("plumbing").SetTitle("Leak").
			SetStatus("assigned").Save(ctx)
		must(err)
		return wo
	}
	clerk := uuid.New()
	q := func(v float64) *float64 { return &v }
	manager := approvals.Actor{UserID: uuid.New(), Name: "Manager", HasPerm: func(string) bool { return true }}

	// Small quote: the default single step.
	wo := newWO("WO-1")
	_, err = s.Act(ctx, wo.ID, Actor{UserID: clerk, Kind: "staff"}, ActionInput{Action: "quote", QuoteAmount: q(8000)})
	must(err)
	if _, err := s.DecideQuote(ctx, wo.ID, approvals.Actor{UserID: clerk, HasPerm: func(string) bool { return true }}, approvals.Approve, ""); err == nil {
		t.Fatal("the person who entered the quote should not approve it")
	}
	got, err := s.DecideQuote(ctx, wo.ID, manager, approvals.Approve, "")
	must(err)
	if got.Status != "approved" || got.QuoteStatus != "approved" {
		t.Fatalf("approved quote: %s %s", got.Status, got.QuoteStatus)
	}

	// Large quote: two steps by role; a rejection at step two sends it back.
	_, err = ap.CreateRule(ctx, approvals.RuleInput{Module: "work_order_quote", MinAmount: decimal.NewFromInt(50000),
		Steps: []approvals.Step{{ApproverRole: "property_manager"}, {ApproverRole: "tenant_admin"}}})
	must(err)
	big := newWO("WO-2")
	_, err = s.Act(ctx, big.ID, Actor{UserID: clerk, Kind: "staff"}, ActionInput{Action: "quote", QuoteAmount: q(90000)})
	must(err)
	got, err = s.DecideQuote(ctx, big.ID, approvals.Actor{UserID: uuid.New(), Roles: []string{"property_manager"}}, approvals.Approve, "")
	must(err)
	if got.Status != "quoted" {
		t.Fatalf("after step one the quote waits: %s", got.Status)
	}
	got, err = s.DecideQuote(ctx, big.ID, approvals.Actor{UserID: uuid.New(), Roles: []string{"tenant_admin"}}, approvals.Reject, "too dear")
	must(err)
	if got.Status != "assigned" || got.QuoteStatus != "rejected" {
		t.Fatalf("rejected quote: %s %s", got.Status, got.QuoteStatus)
	}
	if _, err := s.Act(ctx, big.ID, Actor{UserID: clerk, Kind: "staff"}, ActionInput{Action: "approve_quote"}); err == nil {
		t.Fatal("quotes are never approved through the lifecycle action")
	}
}
