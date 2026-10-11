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
	"github.com/bengobox/maskani-api/internal/ent/gateevent"
	_ "github.com/bengobox/maskani-api/internal/ent/runtime"
	"github.com/bengobox/maskani-api/internal/platform/database"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// TestTableReportsOnPostgres runs every table report on seeded data and checks the figures each
// one is built from. Needs REPORTS_TEST_POSTGRES_URL (a scratch database).
func TestTableReportsOnPostgres(t *testing.T) {
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
	d := func(n int64) decimal.Decimal { return decimal.NewFromInt(n) }
	at := func(day int) time.Time { return time.Date(2026, 9, day, 10, 0, 0, 0, loc) }

	p, err := client.Property.Create().SetCode("MV").SetName("Mvumo").Save(tctx)
	must(err)
	blockA, err := client.Block.Create().SetPropertyID(p.ID).SetCode("A").SetName("Block A").Save(tctx)
	must(err)
	estate, err := client.Fund.Create().SetCode("estate").SetName("Estate").Save(tctx)
	must(err)
	_, err = client.ChargeType.Create().SetCode("water").SetName("Water").SetFundCode("estate").SetChargeGroup("utilities").SetBasis("metered").Save(tctx)
	must(err)
	u1, err := client.Unit.Create().SetPropertyID(p.ID).SetBlockID(blockA.ID).SetCode("A01").Save(tctx)
	must(err)
	u2, err := client.Unit.Create().SetPropertyID(p.ID).SetCode("X01").Save(tctx)
	must(err)
	acc1, err := client.UnitAccount.Create().SetUnitID(u1.ID).SetFundID(estate.ID).SetAccountRef("A01").SetBalance(d(300)).Save(tctx)
	must(err)
	acc2, err := client.UnitAccount.Create().SetUnitID(u2.ID).SetFundID(estate.ID).SetAccountRef("X01").Save(tctx)
	must(err)
	run, err := client.BillingRun.Create().SetPropertyID(p.ID).SetFundID(estate.ID).SetPeriod("2026-09").
		SetInvoiceDate(at(1)).SetDueDate(at(10)).Save(tctx)
	must(err)
	lines := []map[string]any{{"charge_code": "service_charge", "amount": "700.00"}, {"charge_code": "water", "amount": "300.00"}}
	must(client.BillingRunLine.Create().SetRunID(run.ID).SetUnitID(u1.ID).SetUnitAccountID(acc1.ID).SetUnitCode("A01").
		SetLines(lines).SetTotal(d(1000)).SetStatus(billingrunline.StatusIssued).Exec(tctx))
	must(client.BillingRunLine.Create().SetRunID(run.ID).SetUnitID(u2.ID).SetUnitAccountID(acc2.ID).SetUnitCode("X01").
		SetLines(lines).SetTotal(d(1000)).SetStatus(billingrunline.StatusIssued).Exec(tctx))
	must(client.DailyStat.Create().SetPropertyID(p.ID).SetDay(at(15)).SetCollected(d(1700)).SetPaymentsCount(3).Exec(tctx))
	must(client.AccountCollection.Create().SetUnitAccountID(acc1.ID).SetUnitID(u1.ID).SetPropertyID(p.ID).SetFundID(estate.ID).
		SetDay(at(15)).SetAmount(d(700)).SetPaymentsCount(1).Exec(tctx))
	must(client.AccountCollection.Create().SetUnitAccountID(acc2.ID).SetUnitID(u2.ID).SetPropertyID(p.ID).SetFundID(estate.ID).
		SetDay(at(15)).SetAmount(d(1000)).SetPaymentsCount(2).Exec(tctx))
	rev := at(15)
	must(client.ManualPayment.Create().SetUnitAccountID(acc2.ID).SetPropertyID(p.ID).SetAmount(d(500)).SetMethod("bank_transfer").
		SetReference("FT1").SetPaidOn(at(14)).SetStatus("approved").SetSubmittedBy(uuid.New()).SetReviewedAt(rev).Exec(tctx))
	v, err := client.Vendor.Create().SetName("Fixit Ltd").Save(tctx)
	must(err)
	must(client.VendorDocument.Create().SetVendorID(v.ID).SetDocType("kra").SetExpiresAt(time.Now().AddDate(0, 0, 10)).Exec(tctx))
	done := at(3)
	must(client.WorkOrder.Create().SetNumber("WO-1").SetPropertyID(p.ID).SetCategory("plumbing").SetTitle("Leak").SetVendorID(v.ID).
		SetCreatedAt(at(2)).SetRespondedAt(at(2).Add(2 * time.Hour)).SetCompletedAt(done).SetCostAmount(d(4500)).SetStatus("completed").Exec(tctx))
	must(client.WorkOrder.Create().SetNumber("WO-2").SetPropertyID(p.ID).SetCategory("plumbing").SetTitle("Tap").SetVendorID(v.ID).
		SetCreatedAt(at(5)).SetSLABreached(true).SetStatus("assigned").Exec(tctx))
	for i, k := range []string{"entry", "exit", "walk_in_request"} {
		must(client.GateEvent.Create().SetPropertyID(p.ID).SetKind(gateKind(k)).SetOccurredAt(at(6)).SetClientEventID(k + string(rune('a'+i))).Exec(tctx))
	}
	must(client.Incident.Create().SetNumber("IN-1").SetPropertyID(p.ID).SetCategory("theft").SetTitle("Bike").SetSeverity("high").SetOccurredAt(at(7)).Exec(tctx))

	sc := Scope{PropertyID: &p.ID}
	rg := ReportRange{From: "2026-09", To: "2026-09"}
	get := func(kind string) *Table {
		t.Helper()
		tb, err := s.TableReport(tctx, sc, kind, rg)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		return tb
	}
	if r := get("collections_month"); len(r.Rows) != 1 || r.Rows[0]["billed"] != "2000.00" || r.Rows[0]["collected"] != "1700.00" || r.Rows[0]["rate"] != "85" {
		t.Fatalf("collections by month: %+v", r.Rows)
	}
	if r := get("collections_block"); len(r.Rows) != 2 || r.Totals["owing"] != "300.00" || r.Totals["collected"] != "1700.00" {
		t.Fatalf("collections by block: %+v %+v", r.Rows, r.Totals)
	}
	if r := get("collections_charge"); len(r.Rows) != 2 || r.Rows[0]["charge"] != "service_charge" || r.Rows[1]["charge"] != "Water" || r.Rows[0]["billed"] != "1400.00" {
		t.Fatalf("billing by charge: %+v", r.Rows)
	}
	if r := get("payment_channels"); len(r.Rows) != 2 || r.Rows[0]["amount"] != "1200.00" || r.Rows[1]["channel"] != "Bank transfer" {
		t.Fatalf("payment channels: %+v", r.Rows)
	}
	if r := get("maintenance"); len(r.Rows) != 1 || r.Rows[0]["opened"] != int64(2) || r.Rows[0]["breached"] != int64(1) || r.Rows[0]["cost"] != "4500.00" {
		t.Fatalf("maintenance: %+v", r.Rows)
	}
	if r := get("vendors"); len(r.Rows) != 1 || r.Rows[0]["jobs"] != int64(2) || r.Rows[0]["on_time"] != "100" || r.Rows[0]["docs_expiring"] != int64(1) {
		t.Fatalf("vendors: %+v", r.Rows)
	}
	if r := get("security"); len(r.Rows) != 1 || r.Rows[0]["entries"] != int64(1) || r.Rows[0]["walk_ins"] != int64(1) || r.Rows[0]["serious"] != int64(1) {
		t.Fatalf("security: %+v", r.Rows)
	}
	if _, err := s.TableReport(tctx, Scope{All: true}, "water", rg); err == nil {
		t.Fatal("the water trend needs a property")
	}
	if _, err := s.TableReport(tctx, sc, "collections_month", ReportRange{From: "2026-10", To: "2026-01"}); err == nil {
		t.Fatal("a backwards range should be refused")
	}
}

func gateKind(k string) gateevent.Kind { return gateevent.Kind(k) }
