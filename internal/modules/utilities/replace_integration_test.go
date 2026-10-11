package utilities

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
	"github.com/bengobox/maskani-api/internal/ent/meterreading"
	_ "github.com/bengobox/maskani-api/internal/ent/runtime"
	"github.com/bengobox/maskani-api/internal/platform/database"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// TestReplaceMeterOnPostgres: the old meter's last stretch and the new meter's use from its opening
// reading add up to the period's use; a closing reading below the last is refused; a batch records
// what it can and reports the rest. Needs UTILITIES_TEST_POSTGRES_URL (a scratch database).
func TestReplaceMeterOnPostgres(t *testing.T) {
	dsn := os.Getenv("UTILITIES_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("UTILITIES_TEST_POSTGRES_URL not set")
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
	s := NewService(client, zap.NewNop())
	p, err := client.Property.Create().SetCode("SV").SetName("Shaba").Save(ctx)
	must(err)
	u, err := client.Unit.Create().SetPropertyID(p.ID).SetCode("A01").Save(ctx)
	must(err)
	old, err := s.CreateMeter(ctx, MeterInput{PropertyID: p.ID, UnitID: &u.ID, Serial: "OLD-1", InitialReading: 100})
	must(err)
	by := uuid.New()
	if _, err := s.ReplaceMeter(ctx, old.ID, by, ReplaceInput{Period: "2026-10", ClosingReading: 90, NewSerial: "NEW-1"}); err == nil {
		t.Fatal("a closing reading below the last should be refused")
	}
	nm, err := s.ReplaceMeter(ctx, old.ID, by, ReplaceInput{Period: "2026-10", ClosingReading: 130, NewSerial: "NEW-1", OpeningReading: 0})
	must(err)
	if _, err := s.ReplaceMeter(ctx, old.ID, by, ReplaceInput{Period: "2026-10", ClosingReading: 131, NewSerial: "NEW-2"}); err == nil {
		t.Fatal("a replaced meter cannot be replaced again")
	}
	_, err = s.Record(ctx, nm.ID, by, ReadingInput{Period: "2026-10", Reading: 12})
	must(err)
	var total []struct {
		Sum decimal.Decimal `json:"sum"`
	}
	must(client.MeterReading.Query().Where(meterreading.UnitID(u.ID), meterreading.Period("2026-10")).
		Aggregate(ent.Sum(meterreading.FieldConsumption)).Scan(ctx, &total))
	if len(total) != 1 || !total[0].Sum.Equal(decimal.NewFromInt(42)) {
		t.Fatalf("period use should be 30 + 12 = 42, got %v", total)
	}
	res, err := s.RecordBatch(ctx, p.ID, by, "2026-11", []BatchReading{{MeterID: nm.ID, Reading: 20}, {MeterID: old.ID, Reading: 140}})
	must(err)
	if res[0].Error != "" || res[1].Error == "" {
		t.Fatalf("batch: %+v", res)
	}
}
