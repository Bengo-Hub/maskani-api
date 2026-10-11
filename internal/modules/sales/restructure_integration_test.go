package sales

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
	"github.com/bengobox/maskani-api/internal/ent/instalmentschedule"
	"github.com/bengobox/maskani-api/internal/ent/outboxevent"
	_ "github.com/bengobox/maskani-api/internal/ent/runtime"
	"github.com/bengobox/maskani-api/internal/modules/approvals"
	"github.com/bengobox/maskani-api/internal/modules/treasury"
	"github.com/bengobox/maskani-api/internal/platform/database"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
	"github.com/bengobox/maskani-api/internal/shared/otp"
	"github.com/bengobox/maskani-api/internal/shared/secure"
)

// TestRestructureOnPostgres: a new plan covers only what is not yet billed, waits for approval
// (one at a time), is offered to the buyer, and takes effect when they read back the code: the old
// version is superseded, its billed deposit stays on the plan, and a contract in default is cured.
// Needs SALES_TEST_POSTGRES_URL (a scratch database).
func TestRestructureOnPostgres(t *testing.T) {
	dsn := os.Getenv("SALES_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("SALES_TEST_POSTGRES_URL not set")
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
	loc := time.FixedZone("EAT", 3*3600)
	box, err := secure.NewBox("test-secret-key-for-restructure-0123456")
	must(err)
	s := NewService(client, treasury.NewClient("", "", zap.NewNop()), nil, nil, loc, zap.NewNop())
	ap := approvals.NewService(client)
	s.SetApprovals(ap, box)
	officer, manager := uuid.New(), uuid.New()
	d := func(n int64) decimal.Decimal { return decimal.NewFromInt(n) }

	prop, err := client.Property.Create().SetCode("MV").SetName("Mvumo").Save(ctx)
	must(err)
	u, err := client.Unit.Create().SetPropertyID(prop.ID).SetCode("B07").Save(ctx)
	must(err)
	buyer, err := client.Party.Create().SetDisplayName("Jane Buyer").SetPhone("254712000333").Save(ctx)
	must(err)
	sc, err := client.SaleContract.Create().SetContractNumber("SC-0001").SetPropertyID(prop.ID).SetUnitID(u.ID).
		SetPrimaryBuyerID(buyer.ID).SetPrice(d(1_000_000)).SetNetPrice(d(1_000_000)).SetDepositAmount(d(200_000)).
		SetStatus("in_default").SetDefaultSince(time.Now().AddDate(0, 0, -10)).Save(ctx)
	must(err)
	v1, err := client.InstalmentSchedule.Create().SetContractID(sc.ID).SetVersion(1).Save(ctx)
	must(err)
	inv := uuid.New()
	must(client.Instalment.Create().SetScheduleID(v1.ID).SetContractID(sc.ID).SetSeq(1).SetKind("deposit").
		SetDueDate(time.Now().AddDate(0, -2, 0)).SetAmount(d(200_000)).SetStatus("invoiced").SetTreasuryInvoiceID(inv).Exec(ctx))
	for i := 2; i <= 5; i++ {
		must(client.Instalment.Create().SetScheduleID(v1.ID).SetContractID(sc.ID).SetSeq(i).SetKind("instalment").
			SetDueDate(time.Now().AddDate(0, i, 0)).SetAmount(d(200_000)).Exec(ctx))
	}

	first := time.Now().In(loc).AddDate(0, 1, 0).Format("2006-01-02")
	if _, err := s.ProposeRestructure(ctx, sc.ID, officer, "Officer", RestructureInput{Reason: "Job loss", Option: "instalments",
		Frequency: "monthly", TermMonths: 8, FirstDue: time.Now().AddDate(0, 0, -1).Format("2006-01-02")}); err == nil {
		t.Fatal("a first payment in the past is refused")
	}
	plan, err := s.ProposeRestructure(ctx, sc.ID, officer, "Officer", RestructureInput{Reason: "Job loss", Option: "instalments",
		Frequency: "monthly", TermMonths: 8, FirstDue: first})
	must(err)
	if plan.Status != instalmentschedule.StatusProposed || plan.Version != 2 || len(plan.Instalments) != 8 ||
		!plan.Total.Equal(d(800_000)) || plan.ApprovedAt != nil {
		t.Fatalf("proposal: %s v%d %d lines total %s", plan.Status, plan.Version, len(plan.Instalments), plan.Total)
	}
	if _, err := s.ProposeRestructure(ctx, sc.ID, officer, "Officer", RestructureInput{Reason: "Again", Option: "outright", FirstDue: first}); err == nil {
		t.Fatal("one proposal at a time")
	}
	if _, err := s.AcceptRestructure(ctx, plan.ID, Acceptance{Via: "portal", Person: buyer.ID}); err == nil {
		t.Fatal("the buyer cannot accept before approval")
	}

	req, err := ap.Latest(ctx, plan.ID)
	must(err)
	if _, err := ap.Decide(ctx, req.ID, approvals.Actor{UserID: officer, HasPerm: func(string) bool { return true }}, approvals.Approve, ""); err == nil {
		t.Fatal("the proposer cannot approve their own plan")
	}
	_, err = ap.Decide(ctx, req.ID, approvals.Actor{UserID: manager, Name: "Manager", HasPerm: func(string) bool { return true }}, approvals.Approve, "")
	must(err)
	view, err := s.GetContract(ctx, sc.ID)
	must(err)
	if view.PendingPlan == nil || view.PendingPlan.ID != plan.ID || view.PendingPlan.ApprovedAt == nil {
		t.Fatalf("the approved plan waits for the buyer: %+v", view.PendingPlan)
	}
	if n, _ := client.OutboxEvent.Query().Where(outboxevent.EventType(RestructureProposed)).Count(ctx); n != 1 {
		t.Fatalf("the buyer is told once, got %d", n)
	}

	must(s.SendRestructureCode(ctx, plan.ID))
	ev, err := client.OutboxEvent.Query().Where(outboxevent.EventType(otp.Requested)).Only(ctx)
	must(err)
	var envelope struct {
		Payload map[string]any `json:"payload"`
	}
	must(json.Unmarshal(ev.Payload, &envelope))
	if envelope.Payload["purpose"] != "restructure" || envelope.Payload["phone"] != "254712000333" {
		t.Fatalf("code event: %v", envelope.Payload)
	}
	if _, err := s.AcceptRestructure(ctx, plan.ID, Acceptance{Via: "code", Code: "000000", Person: officer}); err == nil {
		t.Fatal("a wrong code is refused")
	}
	done, err := s.AcceptRestructure(ctx, plan.ID, Acceptance{Via: "code", Code: envelope.Payload["otp"].(string), Person: officer})
	must(err)
	if done.Status != instalmentschedule.StatusActive || done.BuyerAcceptedAt == nil {
		t.Fatalf("accepted plan: %s", done.Status)
	}
	if old, _ := client.InstalmentSchedule.Get(ctx, v1.ID); old.Status != instalmentschedule.StatusSuperseded {
		t.Fatalf("old plan: %s", old.Status)
	}
	view, err = s.GetContract(ctx, sc.ID)
	must(err)
	if len(view.Instalments) != 9 || view.Instalments[0].Kind != "deposit" || view.PendingPlan != nil || view.Status != "active" || view.DefaultSince != nil {
		t.Fatalf("contract after: %d lines, first %s, status %s", len(view.Instalments), view.Instalments[0].Kind, view.Status)
	}
	if _, err := s.AcceptRestructure(ctx, plan.ID, Acceptance{Via: "portal", Person: buyer.ID}); err == nil {
		t.Fatal("a plan is accepted once")
	}
}
