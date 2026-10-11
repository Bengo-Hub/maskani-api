package register

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

// TestDefaultPropertyOnPostgres: the console opens on the property of the tenant's head office
// outlet; without one, on the first property by name. Exactly one property is the default.
// Needs REGISTER_TEST_POSTGRES_URL (a scratch database).
func TestDefaultPropertyOnPostgres(t *testing.T) {
	dsn := os.Getenv("REGISTER_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("REGISTER_TEST_POSTGRES_URL not set")
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
	tenantID := uuid.New()
	ctx := tenantguard.With(context.Background(), tenantID)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	s := NewService(client, nil, nil, nil, zap.NewNop())
	defaults := func() []string {
		t.Helper()
		rows, err := s.ListProperties(ctx, nil, true, "")
		must(err)
		var out []string
		for _, r := range rows {
			if r.IsDefault {
				out = append(out, r.Code)
			}
		}
		return out
	}

	_, err = client.Tenant.Create().SetID(tenantID).SetName("Demo").SetSlug("demo-" + tenantID.String()[:8]).Save(ctx)
	must(err)
	branch, err := client.Outlet.Create().SetTenantID(tenantID).SetTenantSlug("demo").SetCode("BR").SetName("Branch").Save(ctx)
	must(err)
	hq, err := client.Outlet.Create().SetTenantID(tenantID).SetTenantSlug("demo").SetCode("HQ").SetName("Head office").SetIsHq(true).Save(ctx)
	must(err)
	_, err = client.Property.Create().SetCode("AAA").SetName("Acacia Court").SetOutletID(branch.ID).Save(ctx)
	must(err)
	if got := defaults(); len(got) != 1 || got[0] != "AAA" {
		t.Fatalf("one property: default %v", got)
	}
	_, err = client.Property.Create().SetCode("ZZZ").SetName("Zebra Gardens").SetOutletID(hq.ID).Save(ctx)
	must(err)
	if got := defaults(); len(got) != 1 || got[0] != "ZZZ" {
		t.Fatalf("head office property should win over the first by name: %v", got)
	}
	must(client.Outlet.UpdateOneID(hq.ID).SetIsHq(false).Exec(ctx))
	if got := defaults(); len(got) != 1 || got[0] != "AAA" {
		t.Fatalf("no head office: first by name, got %v", got)
	}
}
