package main

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
	"github.com/bengobox/maskani-api/internal/ent/property"
	"github.com/bengobox/maskani-api/internal/modules/accounts"
	"github.com/bengobox/maskani-api/internal/modules/billing"
	"github.com/bengobox/maskani-api/internal/modules/reports"
	"github.com/bengobox/maskani-api/internal/modules/sales"
	"github.com/bengobox/maskani-api/internal/modules/sequence"
	"github.com/bengobox/maskani-api/internal/modules/treasury"
	"github.com/bengobox/maskani-api/internal/modules/works"
	"github.com/bengobox/maskani-api/internal/platform/database"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
	"github.com/bengobox/maskani-api/internal/shared/page"
)

// TestSeededEstate checks a tenant seeded by this command: B07 bills exactly 6,450 for last month
// (SRDD 8.2) and the dashboard's grouped SQL runs. Needs TEST_POSTGRES_URL and SEED_TEST_TENANT
// (slug) of a database where seed-tenant has run.
func TestSeededEstate(t *testing.T) {
	dsn, slug := os.Getenv("TEST_POSTGRES_URL"), os.Getenv("SEED_TEST_TENANT")
	if dsn == "" || slug == "" {
		t.Skip("TEST_POSTGRES_URL and SEED_TEST_TENANT not set")
	}
	db, err := database.OpenSQL(dsn, config.PostgresConfig{MaxOpenConns: 2, MaxIdleConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer client.Close()
	ctx := context.Background()
	tn, err := findTenant(ctx, client, slug)
	if err != nil {
		t.Fatal(err)
	}
	tctx := tenantguard.With(ctx, tn.ID)
	prop, err := client.Property.Query().Where(property.Code("SHABA")).Only(tctx)
	if err != nil {
		t.Fatal(err)
	}
	loc := time.FixedZone("EAT", 3*3600)
	log := zap.NewNop()
	tc := treasury.NewClient("", "", log)
	svc := billing.NewService(client, tc, accounts.NewService(client, tc, log), loc, log)
	pv, err := svc.Compute(tctx, prop.ID, "estate", lastPeriod(time.Now(), loc))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range pv.Lines {
		if l.UnitCode == "B07" {
			found = true
			if !l.Total.Equal(decimal.NewFromInt(6450)) {
				t.Fatalf("B07 total = %s, want 6450 (lines %+v)", l.Total, l.Lines)
			}
		}
	}
	if !found || pv.Billable != 29 {
		t.Fatalf("B07 found %v, billable %d (want 29)", found, pv.Billable)
	}
	rep := reports.NewService(client, db, nil, loc, log)
	d, err := rep.Dashboard(tctx, reports.Scope{PropertyID: &prop.ID}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.ArrearsAgeing) != 4 || len(d.CollectionsByWeek) < 4 || d.Units != 40 {
		t.Fatalf("unexpected dashboard: units %d, weeks %d, buckets %d", d.Units, len(d.CollectionsByWeek), len(d.ArrearsAgeing))
	}

	// Keyset lists over the seeded data, limited to the property scope.
	scope := []uuid.UUID{prop.ID}
	p := page.Params{Limit: 2}
	seq := sequence.NewAllocator(client, loc)
	salesSvc := sales.NewService(client, tc, accounts.NewService(client, tc, log), seq, loc, log)
	res, err := salesSvc.ListReservations(tctx, nil, scope, false, "", p)
	if err != nil || len(res.Data) != 1 || res.Data[0].UnitCode != "A16" {
		t.Fatalf("reservations: %+v %v", res, err)
	}
	cs, err := salesSvc.ListContracts(tctx, &prop.ID, nil, false, "", page.Params{Limit: 1})
	if err != nil || len(cs.Data) != 1 || !cs.HasMore {
		t.Fatalf("contracts page: %+v %v", cs, err)
	}
	more, err := salesSvc.ListContracts(tctx, &prop.ID, nil, false, "", page.Params{Limit: 1, HasAfter: true,
		AfterID: cs.Data[0].ID, AfterAt: cs.Data[0].CreatedAt})
	if err != nil || len(more.Data) != 1 || more.Data[0].ID == cs.Data[0].ID {
		t.Fatalf("contracts second page: %+v %v", more, err)
	}
	if _, err := rep.Arrears(tctx, nil, scope, false, reports.ArrearsFilter{}, page.DecimalParams{Limit: 5}); err != nil {
		t.Fatalf("arrears: %v", err)
	}
	ws := works.NewService(client, seq, log)
	vs, err := ws.ListVendors(tctx, "", page.Params{Limit: 10})
	if err != nil || len(vs.Data) != 3 {
		t.Fatalf("vendors: %d %v", len(vs.Data), err)
	}
	for _, v := range vs.Data {
		if v.Name != "Shaba Guard Services Ltd" {
			continue
		}
		vd, err := ws.GetVendor(tctx, v.ID, scope, false)
		if err != nil || len(vd.Personnel) != 2 || !vd.Personnel[0].HasPIN || vd.NextExpiry == nil {
			t.Fatalf("vendor detail: %+v %v", vd, err)
		}
	}
}
