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
	"github.com/bengobox/maskani-api/internal/ent/salecontract"
	_ "github.com/bengobox/maskani-api/internal/ent/runtime"
	"github.com/bengobox/maskani-api/internal/platform/database"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// TestSalesReportsOnPostgres runs the sales position, instalment receivables and handover and title
// reports on real rows: overdue only counts billed instalments past due, a superseded plan's
// unbilled lines are left out, and open snags and done stages are counted. Needs
// REPORTS_TEST_POSTGRES_URL (a scratch database).
func TestSalesReportsOnPostgres(t *testing.T) {
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
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	loc := time.FixedZone("EAT", 3*3600)
	s := NewService(client, db, nil, loc, zap.NewNop())
	ctx := tenantguard.With(context.Background(), uuid.New())
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	d := func(n int64) decimal.Decimal { return decimal.NewFromInt(n) }
	now := time.Now().In(loc)

	p, err := client.Property.Create().SetCode("MV").SetName("Mvumo").Save(ctx)
	must(err)
	sc := Scope{PropertyID: &p.ID}
	contract := func(code, number, status string, paid int64) *ent.SaleContract {
		u, err := client.Unit.Create().SetPropertyID(p.ID).SetCode(code).Save(ctx)
		must(err)
		b, err := client.Party.Create().SetDisplayName("Buyer " + code).Save(ctx)
		must(err)
		c, err := client.SaleContract.Create().SetContractNumber(number).SetPropertyID(p.ID).SetUnitID(u.ID).SetPrimaryBuyerID(b.ID).
			SetPrice(d(1000)).SetNetPrice(d(1000)).SetPaidTotal(d(paid)).SetStatus(salecontract.Status(status)).Save(ctx)
		must(err)
		return c
	}
	line := func(sch *ent.InstalmentSchedule, seq int, due time.Time, amount, paid int64, billed bool) {
		c := client.Instalment.Create().SetScheduleID(sch.ID).SetContractID(sch.ContractID).SetSeq(seq).SetKind("instalment").
			SetDueDate(due).SetAmount(d(amount)).SetPaidAmount(d(paid))
		if billed {
			c.SetTreasuryInvoiceID(uuid.New()).SetStatus("invoiced")
		}
		if paid >= amount {
			c.SetStatus("paid")
		}
		must(c.Exec(ctx))
	}

	// A: 400 paid of 1000; 300 billed and overdue, one more line next month; an old plan's unbilled line ignored.
	a := contract("A01", "SC-A", "active", 400)
	old, err := client.InstalmentSchedule.Create().SetContractID(a.ID).SetVersion(1).SetStatus("superseded").Save(ctx)
	must(err)
	line(old, 1, now.AddDate(0, -2, 0), 400, 400, true)
	line(old, 2, now.AddDate(0, 3, 0), 600, 0, false)
	cur, err := client.InstalmentSchedule.Create().SetContractID(a.ID).SetVersion(2).Save(ctx)
	must(err)
	line(cur, 1, now.AddDate(0, 0, -5), 300, 0, true)
	line(cur, 2, now.AddDate(0, 1, 0), 300, 0, false)
	// B: fully paid, handed over with one open snag and one title stage done.
	b := contract("B01", "SC-B", "handed_over", 1000)
	must(client.Handover.Create().SetContractID(b.ID).SetUnitID(b.UnitID).SetStatus("completed").SetCompletedAt(now).
		SetSnagItems([]map[string]any{{"id": "1", "status": "open"}, {"id": "2", "status": "fixed"}}).Exec(ctx))
	must(client.TitleStage.Create().SetContractID(b.ID).SetUnitID(b.UnitID).SetStage("stamp_duty").SetStatus("done").Exec(ctx))

	pos, err := s.TableReport(ctx, sc, "sales_position", ReportRange{})
	must(err)
	if len(pos.Rows) != 2 || pos.Rows[0]["contract"] != "SC-A" || pos.Rows[0]["overdue"] != "300.00" ||
		pos.Rows[0]["balance"] != "600.00" || pos.Rows[0]["next_due"] == nil {
		t.Fatalf("position: %+v", pos.Rows)
	}
	rec, err := s.TableReport(ctx, sc, "instalment_receivables", ReportRange{From: now.AddDate(0, -3, 0).Format("2006-01"), To: now.AddDate(0, 4, 0).Format("2006-01")})
	must(err)
	due := decimal.Zero
	for _, r := range rec.Rows {
		v, _ := decimal.NewFromString(r["due"].(string))
		due = due.Add(v)
	}
	if !due.Equal(d(1000)) {
		t.Fatalf("receivables fall due %s, want 1000 (the superseded unbilled 600 left out): %+v", due, rec.Rows)
	}
	ht, err := s.TableReport(ctx, sc, "handover_title", ReportRange{})
	must(err)
	if len(ht.Rows) != 1 || ht.Rows[0]["handover"] != "completed" || ht.Rows[0]["open_snags"] != int64(1) || ht.Rows[0]["stages_done"] != int64(1) {
		t.Fatalf("handover and title: %+v", ht.Rows)
	}
}
