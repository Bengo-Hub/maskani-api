package works

import (
	"context"
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
	"github.com/bengobox/maskani-api/internal/ent/servicevisit"
	"github.com/bengobox/maskani-api/internal/ent/workorder"
	_ "github.com/bengobox/maskani-api/internal/ent/runtime"
	"github.com/bengobox/maskani-api/internal/modules/sequence"
	"github.com/bengobox/maskani-api/internal/platform/database"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// TestVisitsAndPreventiveOnPostgres: a daily plan gets a week of visits, each day once however
// often the job runs; a visit whose day passed unrecorded becomes missed; recording a visit keeps
// its evidence. A preventive plan due within the week raises one work order assigned to its vendor
// and moves to its next date. Needs WORKS_TEST_POSTGRES_URL (a scratch database).
func TestVisitsAndPreventiveOnPostgres(t *testing.T) {
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
	sys := tenantguard.System(ctx)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	s := NewService(client, sequence.NewAllocator(client, time.UTC), zap.NewNop())
	prop, err := client.Property.Create().SetCode("MV").SetName("Mvumo").Save(ctx)
	must(err)
	v, err := client.Vendor.Create().SetName("CleanCo").Save(ctx)
	must(err)
	today := time.Now().UTC().Truncate(24 * time.Hour)
	c, err := s.CreateContract(ctx, v.ID, ContractInput{PropertyID: prop.ID, ServiceCategory: "cleaning", FeeBasis: "per_visit",
		FeeAmount: decimal.NewFromInt(2000), StartsOn: today.AddDate(0, -1, 0)})
	must(err)
	if _, err := s.CreateSchedule(ctx, c.ID, ScheduleInput{Name: "Bad", Frequency: "weekly", Days: []int{9}}); err == nil {
		t.Fatal("weekly days are 0 to 6")
	}
	plan, err := s.CreateSchedule(ctx, c.ID, ScheduleInput{Name: "Common areas", Frequency: "daily", Checklist: []map[string]any{{"item": "Stairs mopped"}}})
	must(err)

	planned, _, err := s.PlanVisits(sys, []uuid.UUID{tenant})
	must(err)
	if planned != visitHorizon+1 {
		t.Fatalf("a week of daily visits: %d", planned)
	}
	again, _, err := s.PlanVisits(sys, []uuid.UUID{tenant})
	must(err)
	if again != 0 {
		t.Fatalf("days are planned once, got %d more", again)
	}
	past, err := client.ServiceVisit.Create().SetScheduleID(plan.ID).SetContractID(c.ID).SetVendorID(v.ID).SetPropertyID(prop.ID).
		SetDueAt(today.AddDate(0, 0, -2)).Save(ctx)
	must(err)
	_, missed, err := s.PlanVisits(sys, []uuid.UUID{tenant})
	must(err)
	if missed != 1 {
		t.Fatalf("missed: %d", missed)
	}
	if got, _ := client.ServiceVisit.Get(ctx, past.ID); got.Status != servicevisit.StatusMissed {
		t.Fatalf("past visit: %s", got.Status)
	}
	visits, err := s.Visits(ctx, VisitFilter{PropertyID: prop.ID, From: today, To: today})
	must(err)
	if len(visits) != 1 {
		t.Fatalf("today's visits: %d", len(visits))
	}
	rec, err := s.RecordVisit(ctx, visits[0].ID, VisitInput{Status: "completed", Photos: []string{"maskani/x/works/2026/10/p.jpg"},
		Checklist: []map[string]any{{"item": "Stairs mopped", "done": true}}})
	must(err)
	if rec.Status != servicevisit.StatusCompleted || rec.CheckedInAt == nil || len(rec.Photos) != 1 {
		t.Fatalf("recorded: %+v", rec.Status)
	}
	if _, err := s.Visits(ctx, VisitFilter{PropertyID: prop.ID, From: today, To: today.AddDate(0, 3, 0)}); err == nil {
		t.Fatal("ranges are at most 62 days")
	}

	vid := v.ID
	m, err := s.CreateMaintenance(ctx, MaintenanceInput{PropertyID: prop.ID, Title: "Service the borehole pump", Category: "plumbing",
		Frequency: "quarterly", NextDueAt: today.AddDate(0, 0, 3), DefaultVendorID: &vid})
	must(err)
	_, err = s.CreateMaintenance(ctx, MaintenanceInput{PropertyID: prop.ID, Title: "Lift inspection", Category: "lifts",
		Frequency: "annual", NextDueAt: today.AddDate(0, 2, 0)})
	must(err)
	raised, err := s.RaisePreventive(sys, []uuid.UUID{tenant})
	must(err)
	if raised != 1 {
		t.Fatalf("raised: %d", raised)
	}
	wo, err := client.WorkOrder.Query().Where(workorder.MaintenanceScheduleID(m.ID)).Only(ctx)
	must(err)
	if wo.Source != workorder.SourceSchedule || wo.VendorID == nil || *wo.VendorID != v.ID || wo.AssigneeKind != workorder.AssigneeKindVendor {
		t.Fatalf("preventive work order: %s %v", wo.Source, wo.VendorID)
	}
	if got, _ := client.MaintenanceSchedule.Get(ctx, m.ID); !got.NextDueAt.Equal(m.NextDueAt.AddDate(0, 3, 0)) {
		t.Fatalf("next due: %s", got.NextDueAt)
	}
	if n, _ := s.RaisePreventive(sys, []uuid.UUID{tenant}); n != 0 {
		t.Fatal("raised once per period")
	}
}
