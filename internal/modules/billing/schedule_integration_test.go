package billing

import (
	"context"
	"os"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/config"
	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/chargetype"
	"github.com/bengobox/maskani-api/internal/ent/outboxevent"
	_ "github.com/bengobox/maskani-api/internal/ent/runtime"
	"github.com/bengobox/maskani-api/internal/platform/database"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// TestScheduleOnPostgres runs a billing schedule against a real Postgres: a property with a
// metered charge and an unread meter waits on the billing day and alerts once a day. Needs
// BILLING_TEST_POSTGRES_URL pointing at a scratch database (its schema is created).
func TestScheduleOnPostgres(t *testing.T) {
	dsn := os.Getenv("BILLING_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("BILLING_TEST_POSTGRES_URL not set")
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
	s := NewService(client, nil, nil, loc, zap.NewNop())
	tid := uuid.New()
	tctx := tenantguard.With(ctx, tid)

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(client.TenantSetting.Create().SetBillingDay(1).SetReadingWindowEnd(28).Exec(tctx))
	must(client.Fund.Create().SetCode("estate").SetName("Estate").Exec(tctx))
	must(client.ChargeType.Create().SetCode("water").SetName("Water").SetChargeGroup(chargetype.ChargeGroupUtilities).
		SetBasis(chargetype.BasisMetered).SetFundCode("estate").Exec(tctx))
	p, err := client.Property.Create().SetCode("SV").SetName("Shaba Village").Save(tctx)
	must(err)
	u, err := client.Unit.Create().SetPropertyID(p.ID).SetCode("A01").Save(tctx)
	must(err)
	must(client.Meter.Create().SetPropertyID(p.ID).SetUnitID(u.ID).SetSerial("M-1").Exec(tctx))

	if _, err := s.SaveSchedule(tctx, p.ID, ScheduleInput{Enabled: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	v, err := s.ScheduleStatus(tctx, p.ID)
	must(err)
	if !v.Metered || v.MissingCount != 1 || v.Missing[0].UnitCode != "A01" {
		t.Fatalf("one unread meter expected: %+v", v)
	}

	// On or after the billing day with policy wait: one alert, not a run, and none again today.
	if _, err := s.RunSchedules(ctx, []uuid.UUID{tid}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunSchedules(ctx, []uuid.UUID{tid}); err != nil {
		t.Fatal(err)
	}
	n, err := client.OutboxEvent.Query().Where(outboxevent.EventType("billing.readings_missing")).Count(tctx)
	must(err)
	if n != 1 {
		t.Fatalf("one readings_missing alert expected, got %d", n)
	}
	if runs, _ := client.BillingRun.Query().Count(tctx); runs != 0 {
		t.Fatalf("no run while readings are missing, got %d", runs)
	}
}

func ptr[T any](v T) *T { return &v }
