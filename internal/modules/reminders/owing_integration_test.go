package reminders

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

// TestOwingOnPostgres checks the age the ladder counts from: the due date of the oldest bill the
// balance still covers. Needs REMINDERS_TEST_POSTGRES_URL pointing at a scratch database.
func TestOwingOnPostgres(t *testing.T) {
	dsn := os.Getenv("REMINDERS_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("REMINDERS_TEST_POSTGRES_URL not set")
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
	tid := uuid.New()
	tctx := tenantguard.With(ctx, tid)
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
	acc, err := client.UnitAccount.Create().SetUnitID(u.ID).SetFundID(f.ID).SetAccountRef("A01").SetBalance(decimal.NewFromInt(250)).Save(tctx)
	must(err)
	due := map[string]time.Time{"2026-08": time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC), "2026-09": time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)}
	for per, d := range due {
		run, err := client.BillingRun.Create().SetPropertyID(p.ID).SetFundID(f.ID).SetPeriod(per).SetInvoiceDate(d.AddDate(0, 0, -9)).SetDueDate(d).Save(tctx)
		must(err)
		must(client.BillingRunLine.Create().SetRunID(run.ID).SetUnitID(u.ID).SetUnitAccountID(acc.ID).SetUnitCode("A01").
			SetLines([]map[string]any{}).SetTotal(decimal.NewFromInt(200)).SetStatus(billingrunline.StatusIssued).Exec(tctx))
	}
	s := NewService(client, db, nil, time.UTC, zap.NewNop())
	// 250 owed against two bills of 200: the newest is unpaid, 50 of the older too, so the age
	// counts from August.
	got, err := s.owing(tctx, tid)
	must(err)
	if len(got) != 1 || !got[0].OldestDue.Equal(due["2026-08"]) {
		t.Fatalf("owing with 250: %+v", got)
	}
	// 200 owed covers the September bill only.
	must(client.UnitAccount.UpdateOneID(acc.ID).SetBalance(decimal.NewFromInt(200)).Exec(tctx))
	got, err = s.owing(tctx, tid)
	must(err)
	if len(got) != 1 || !got[0].OldestDue.Equal(due["2026-09"]) {
		t.Fatalf("owing with 200: %+v", got)
	}
}
