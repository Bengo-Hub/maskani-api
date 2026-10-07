package tenantguard_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/config"
	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/property"
	_ "github.com/bengobox/maskani-api/internal/ent/runtime"
	"github.com/bengobox/maskani-api/internal/platform/database"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// TestCrossTenantIsolation proves the Ent guard: a tenant never reads or writes another tenant's
// rows, and a query with no tenant fails closed. Needs a migrated database in TEST_POSTGRES_URL.
func TestCrossTenantIsolation(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_URL not set")
	}
	db, err := database.OpenSQL(dsn, config.PostgresConfig{MaxOpenConns: 2, MaxIdleConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer client.Close()

	bg := context.Background()
	a, b := uuid.New(), uuid.New()
	ctxA, ctxB := tenantguard.With(bg, a), tenantguard.With(bg, b)
	code := "ISO" + uuid.NewString()[:6]

	pa, err := client.Property.Create().SetCode(code).SetName("Tenant A estate").Save(ctxA)
	if err != nil {
		t.Fatal(err)
	}
	if pa.TenantID != a {
		t.Fatalf("create was not stamped with the context tenant: %s", pa.TenantID)
	}
	defer client.Property.DeleteOneID(pa.ID).Exec(ctxA) //nolint:errcheck

	// Same code in tenant B is allowed (per-tenant uniqueness) and invisible to A.
	pb, err := client.Property.Create().SetCode(code).SetName("Tenant B estate").Save(ctxB)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Property.DeleteOneID(pb.ID).Exec(ctxB) //nolint:errcheck

	if n, err := client.Property.Query().Where(property.Code(code)).Count(ctxA); err != nil || n != 1 {
		t.Fatalf("tenant A sees %d rows (err %v), want 1", n, err)
	}
	if _, err := client.Property.Get(ctxB, pa.ID); !ent.IsNotFound(err) {
		t.Fatalf("tenant B read tenant A's property: %v", err)
	}
	if n, err := client.Property.Update().Where(property.ID(pa.ID)).SetName("hijacked").Save(ctxB); err != nil || n != 0 {
		t.Fatalf("tenant B updated %d of tenant A's rows (err %v)", n, err)
	}
	if n, err := client.Property.Delete().Where(property.ID(pa.ID)).Exec(ctxB); err != nil || n != 0 {
		t.Fatalf("tenant B deleted %d of tenant A's rows (err %v)", n, err)
	}
	if _, err := client.Property.Create().SetTenantID(a).SetCode(code + "X").SetName("spoof").Save(ctxB); !errors.Is(err, tenantguard.ErrTenantMismatch) {
		t.Fatalf("spoofed tenant_id on create not rejected: %v", err)
	}
	if _, err := client.Property.Query().Count(bg); !errors.Is(err, tenantguard.ErrNoTenant) {
		t.Fatalf("query without tenant did not fail closed: %v", err)
	}
	if n, err := client.Property.Query().Where(property.Code(code)).Count(tenantguard.System(bg)); err != nil || n != 2 {
		t.Fatalf("system context sees %d rows (err %v), want 2", n, err)
	}
}
