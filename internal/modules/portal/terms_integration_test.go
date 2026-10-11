package portal

import (
	"context"
	"os"
	"testing"

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

// TestTermsHistoryOnPostgres: each acceptance sets the current version on every party of the
// caller and adds one line to the history, read back once per acceptance, newest first. Needs
// PORTAL_TEST_POSTGRES_URL (a scratch database).
func TestTermsHistoryOnPostgres(t *testing.T) {
	dsn := os.Getenv("PORTAL_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("PORTAL_TEST_POSTGRES_URL not set")
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
	a, err := client.Party.Create().SetDisplayName("Owner").Save(ctx)
	must(err)
	b, err := client.Party.Create().SetDisplayName("Owner as buyer").SetConsents(map[string]any{"marketing": true}).Save(ctx)
	must(err)
	ids := []uuid.UUID{a.ID, b.ID}

	if err := s.AcceptTerms(ctx, ids, Acceptance{}); err == nil {
		t.Fatal("a version is required")
	}
	must(s.AcceptTerms(ctx, ids, Acceptance{Version: "2026-01", PrivacyVersion: "p1", IP: "10.0.0.1"}))
	must(s.AcceptTerms(ctx, ids, Acceptance{Version: "2026-10", PrivacyVersion: "p2", IP: "10.0.0.2"}))

	if v := s.AcceptedTerms(ctx, ids); v != "2026-10" {
		t.Fatalf("accepted version %q", v)
	}
	h, err := s.TermsHistory(ctx, ids)
	must(err)
	if len(h) != 2 || h[0].Version != "2026-10" || h[0].PrivacyVersion != "p2" || h[0].IP != "10.0.0.2" || h[1].Version != "2026-01" {
		t.Fatalf("history: %+v", h)
	}
	got, err := client.Party.Get(ctx, b.ID)
	must(err)
	if got.Consents["marketing"] != true {
		t.Fatal("other consents must be kept")
	}
}
