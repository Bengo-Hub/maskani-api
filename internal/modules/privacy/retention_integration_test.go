package privacy

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
	_ "github.com/bengobox/maskani-api/internal/ent/runtime"
	"github.com/bengobox/maskani-api/internal/platform/database"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// TestRetentionOnPostgres: only rows past the schedule go; a banned visitor and one a kept pass
// still points at stay. Needs PRIVACY_TEST_POSTGRES_URL (a scratch database).
func TestRetentionOnPostgres(t *testing.T) {
	dsn := os.Getenv("PRIVACY_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("PRIVACY_TEST_POSTGRES_URL not set")
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
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	s := NewService(client, nil, nil, zap.NewNop())
	prop := uuid.New()
	now := time.Now()
	old, recent := now.AddDate(0, 0, -100), now.AddDate(0, 0, -10)

	for _, at := range []time.Time{old, recent} {
		must(client.GateEvent.Create().SetPropertyID(prop).SetKind("entry").SetOccurredAt(at).SetClientEventID(uuid.NewString()).Exec(ctx))
		must(client.PatrolScan.Create().SetPropertyID(prop).SetCheckpointID(uuid.New()).SetScannedAt(at).SetClientEventID(uuid.NewString()).Exec(ctx))
		must(client.VisitorPass.Create().SetPropertyID(prop).SetVisitorName("Guest").SetCodeHash(uuid.NewString()).
			SetValidFrom(at.Add(-time.Hour)).SetValidTo(at).Exec(ctx))
	}
	lapsed, err := client.Visitor.Create().SetPropertyID(prop).SetName("Lapsed").SetLastVisitAt(now.AddDate(-2, 0, 0)).Save(ctx)
	must(err)
	banned, err := client.Visitor.Create().SetPropertyID(prop).SetName("Banned").SetStatus("banned").SetLastVisitAt(now.AddDate(-2, 0, 0)).Save(ctx)
	must(err)
	expected, err := client.Visitor.Create().SetPropertyID(prop).SetName("Coming back").SetLastVisitAt(now.AddDate(-2, 0, 0)).Save(ctx)
	must(err)
	must(client.VisitorPass.Create().SetPropertyID(prop).SetVisitorName("Coming back").SetCodeHash(uuid.NewString()).
		SetValidFrom(now).SetValidTo(now.Add(24 * time.Hour)).SetVisitorID(expected.ID).Exec(ctx))
	must(client.AuditLog.Create().SetTenantID(tenant).SetActorUserID(uuid.New()).SetAction("x").SetTargetType("x").
		SetTargetID(uuid.New()).SetCreatedAt(now.AddDate(-3, 0, 0)).Exec(ctx))
	must(client.AuditLog.Create().SetTenantID(tenant).SetActorUserID(uuid.New()).SetAction("y").SetTargetType("y").
		SetTargetID(uuid.New()).Exec(ctx))

	got, err := s.PurgeExpired(ctx)
	must(err)
	want := Purged{"gate_events": 1, "patrol_scans": 1, "visitor_passes": 1, "visitors": 1, "audit_logs": 1}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s: removed %d, want %d (%v)", k, got[k], v, got)
		}
	}
	if _, err := client.Visitor.Get(ctx, lapsed.ID); !ent.IsNotFound(err) {
		t.Fatal("the lapsed visitor goes")
	}
	for _, id := range []uuid.UUID{banned.ID, expected.ID} {
		if _, err := client.Visitor.Get(ctx, id); err != nil {
			t.Fatalf("visitor %s must stay: %v", id, err)
		}
	}
	again, err := s.PurgeExpired(ctx)
	must(err)
	for k, v := range again {
		if v != 0 {
			t.Fatalf("a second run removes nothing, got %s=%d", k, v)
		}
	}
}
