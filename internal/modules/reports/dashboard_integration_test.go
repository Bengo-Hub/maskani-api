package reports

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
	"github.com/bengobox/maskani-api/internal/ent/billingrunline"
	_ "github.com/bengobox/maskani-api/internal/ent/runtime"
	"github.com/bengobox/maskani-api/internal/platform/database"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// TestDashboardFiltersOnPostgres checks that the dashboard's month range, block and fund narrow
// billed, outstanding, units and the weekly and ageing SQL. Needs REPORTS_TEST_POSTGRES_URL
// pointing at a scratch database (its schema is created).
func TestDashboardFiltersOnPostgres(t *testing.T) {
	dsn := os.Getenv("REPORTS_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("REPORTS_TEST_POSTGRES_URL not set")
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
	loc := time.FixedZone("EAT", 3*3600)
	s := NewService(client, db, nil, loc, zap.NewNop())
	tctx := tenantguard.With(ctx, uuid.New())
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	p, err := client.Property.Create().SetCode("SV").SetName("Shaba").Save(tctx)
	must(err)
	blockA, err := client.Block.Create().SetPropertyID(p.ID).SetCode("A").SetName("Block A").Save(tctx)
	must(err)
	blockB, err := client.Block.Create().SetPropertyID(p.ID).SetCode("B").SetName("Block B").Save(tctx)
	must(err)
	estate, err := client.Fund.Create().SetCode("estate").SetName("Estate").Save(tctx)
	must(err)
	sales, err := client.Fund.Create().SetCode("sales").SetName("Sales").Save(tctx)
	must(err)
	ua, err := client.Unit.Create().SetPropertyID(p.ID).SetBlockID(blockA.ID).SetCode("A01").Save(tctx)
	must(err)
	ub, err := client.Unit.Create().SetPropertyID(p.ID).SetBlockID(blockB.ID).SetCode("B01").Save(tctx)
	must(err)
	accA, err := client.UnitAccount.Create().SetUnitID(ua.ID).SetFundID(estate.ID).SetAccountRef("A01").SetBalance(decimal.NewFromInt(500)).Save(tctx)
	must(err)
	accB, err := client.UnitAccount.Create().SetUnitID(ub.ID).SetFundID(sales.ID).SetAccountRef("S-B01").SetBalance(decimal.NewFromInt(900)).Save(tctx)
	must(err)

	day := func(m int) time.Time { return time.Date(2026, time.Month(m), 1, 0, 0, 0, 0, loc) }
	line := func(run *ent.BillingRun, u *ent.Unit, acc *ent.UnitAccount, total int64) {
		must(client.BillingRunLine.Create().SetRunID(run.ID).SetUnitID(u.ID).SetUnitAccountID(acc.ID).SetUnitCode(u.Code).
			SetLines([]map[string]any{}).SetTotal(decimal.NewFromInt(total)).SetStatus(billingrunline.StatusIssued).Exec(tctx))
	}
	for m, amt := range map[int]int64{8: 100, 9: 200} {
		run, err := client.BillingRun.Create().SetPropertyID(p.ID).SetFundID(estate.ID).SetPeriod(day(m).Format("2006-01")).
			SetInvoiceDate(day(m)).SetDueDate(day(m).AddDate(0, 0, 9)).Save(tctx)
		must(err)
		line(run, ua, accA, amt)
	}
	srun, err := client.BillingRun.Create().SetPropertyID(p.ID).SetFundID(sales.ID).SetPeriod("2026-09").
		SetInvoiceDate(day(9)).SetDueDate(day(9).AddDate(0, 0, 9)).Save(tctx)
	must(err)
	line(srun, ub, accB, 1000)

	sc := Scope{PropertyID: &p.ID}
	check := func(name string, f DashboardFilter, billed, outstanding int64, units int) {
		t.Helper()
		d, err := s.Dashboard(tctx, sc, f)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !d.Billed.Equal(decimal.NewFromInt(billed)) || !d.Outstanding.Equal(decimal.NewFromInt(outstanding)) || d.Units != units {
			t.Fatalf("%s: billed %s outstanding %s units %d", name, d.Billed, d.Outstanding, d.Units)
		}
		var weekly decimal.Decimal
		for _, w := range d.CollectionsByWeek {
			weekly = weekly.Add(w.Billed)
		}
		if !weekly.Equal(decimal.NewFromInt(billed)) {
			t.Fatalf("%s: weekly billed %s, want %d", name, weekly, billed)
		}
		var aged decimal.Decimal
		for _, b := range d.ArrearsAgeing {
			aged = aged.Add(b.Amount)
		}
		if !aged.Equal(decimal.NewFromInt(outstanding)) {
			t.Fatalf("%s: ageing total %s, want %d", name, aged, outstanding)
		}
	}
	check("one month", DashboardFilter{From: "2026-09", To: "2026-09"}, 1200, 1400, 2)
	check("two months", DashboardFilter{From: "2026-08", To: "2026-09"}, 1300, 1400, 2)
	check("block A", DashboardFilter{From: "2026-08", To: "2026-09", BlockID: &blockA.ID}, 300, 500, 1)
	check("sales fund", DashboardFilter{From: "2026-08", To: "2026-09", FundID: &sales.ID}, 1000, 900, 2)
	// Collections by block and fund come from the per-account totals.
	sep := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	must(client.AccountCollection.Create().SetUnitAccountID(accA.ID).SetUnitID(ua.ID).SetPropertyID(p.ID).SetFundID(estate.ID).
		SetDay(sep).SetAmount(decimal.NewFromInt(150)).SetPaymentsCount(1).Exec(tctx))
	must(client.AccountCollection.Create().SetUnitAccountID(accB.ID).SetUnitID(ub.ID).SetPropertyID(p.ID).SetFundID(sales.ID).
		SetDay(sep).SetAmount(decimal.NewFromInt(400)).SetPaymentsCount(1).Exec(tctx))
	s.Invalidate(uuid.Nil)
	for name, tc := range map[string]struct {
		f    DashboardFilter
		want int64
	}{
		"block A collected":    {DashboardFilter{From: "2026-09", To: "2026-09", BlockID: &blockA.ID}, 150},
		"sales fund collected": {DashboardFilter{From: "2026-09", To: "2026-09", FundID: &sales.ID}, 400},
	} {
		d, err := s.Dashboard(tctx, sc, tc.f)
		must(err)
		var weekly decimal.Decimal
		for _, w := range d.CollectionsByWeek {
			weekly = weekly.Add(w.Collected)
		}
		if !d.Collected.Equal(decimal.NewFromInt(tc.want)) || !weekly.Equal(decimal.NewFromInt(tc.want)) || d.CollectionsScope != "accounts" {
			t.Fatalf("%s: collected %s weekly %s scope %s", name, d.Collected, weekly, d.CollectionsScope)
		}
	}
	if _, err := s.Dashboard(tctx, sc, DashboardFilter{From: "2025-01", To: "2026-09"}); err == nil {
		t.Fatal("a range over 12 months should be refused")
	}
}
