package works

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
	"github.com/bengobox/maskani-api/internal/ent/outboxevent"
	_ "github.com/bengobox/maskani-api/internal/ent/runtime"
	"github.com/bengobox/maskani-api/internal/ent/vendordocument"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/platform/database"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// TestRenewedDocumentOnPostgres: uploading a document of the same type replaces the older one, so a
// renewed permit no longer counts as expired in the vendor view, the shared predicate or the expiry
// alerts. Needs WORKS_TEST_POSTGRES_URL (a scratch database).
func TestRenewedDocumentOnPostgres(t *testing.T) {
	dsn := os.Getenv("WORKS_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("WORKS_TEST_POSTGRES_URL not set")
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
	s := NewService(client, nil, zap.NewNop())
	v, err := client.Vendor.Create().SetName("Guarding Co").Save(ctx)
	must(err)
	now := time.Now()
	expired, err := client.VendorDocument.Create().SetVendorID(v.ID).SetDocType("insurance").
		SetExpiresAt(now.AddDate(0, 0, -20)).SetCreatedAt(now.AddDate(-1, 0, 0)).Save(ctx)
	must(err)
	renewed, err := client.VendorDocument.Create().SetVendorID(v.ID).SetDocType("insurance").
		SetExpiresAt(now.AddDate(1, 0, 0)).Save(ctx)
	must(err)
	permit, err := client.VendorDocument.Create().SetVendorID(v.ID).SetDocType("business_permit").
		SetExpiresAt(now.AddDate(0, 0, 10)).Save(ctx)
	must(err)

	got, err := s.GetVendor(ctx, v.ID, nil, true)
	must(err)
	if got.ExpiredDocs != 0 || len(got.ReplacedDocs) != 1 || got.ReplacedDocs[0] != expired.ID ||
		got.NextExpiry == nil || !got.NextExpiry.Truncate(time.Second).Equal(permit.ExpiresAt.Truncate(time.Second)) || len(got.Documents) != 3 {
		t.Fatalf("vendor view: expired %d replaced %v next %v docs %d", got.ExpiredDocs, got.ReplacedDocs, got.NextExpiry, len(got.Documents))
	}
	ids, err := client.VendorDocument.Query().Where(CurrentDocument()).IDs(ctx)
	must(err)
	if len(ids) != 2 || !containsID(ids, renewed.ID) || !containsID(ids, permit.ID) {
		t.Fatalf("current documents: %v", ids)
	}

	// Alerts go out for the permit only: the replaced insurance is never alerted.
	n, err := s.AlertExpiring(tenantguard.System(context.Background()), []uuid.UUID{tenantID})
	must(err)
	if n != 1 {
		t.Fatalf("alerts sent: %d", n)
	}
	if old, _ := client.VendorDocument.Get(ctx, expired.ID); old.LastAlertDays != nil {
		t.Fatal("a replaced document is not alerted")
	}
	if c, _ := client.OutboxEvent.Query().Where(outboxevent.EventType(events.VendorDocumentExpiring)).Count(ctx); c != 1 {
		t.Fatalf("expiry events: %d", c)
	}
	// Once the last (expired) alert is sent, the document leaves the job's batch for good.
	must(client.VendorDocument.UpdateOneID(permit.ID).SetExpiresAt(now.AddDate(0, 0, -1)).Exec(ctx))
	_, err = s.AlertExpiring(tenantguard.System(context.Background()), []uuid.UUID{tenantID})
	must(err)
	left, err := client.VendorDocument.Query().Where(vendordocument.Or(vendordocument.LastAlertDaysIsNil(), vendordocument.LastAlertDaysGTE(0)),
		vendordocument.ExpiresAtNotNil(), CurrentDocument()).Count(ctx)
	must(err)
	if left != 1 { // only the renewed insurance, a year away
		t.Fatalf("documents still in the alert batch: %d", left)
	}
}

func containsID(ids []uuid.UUID, id uuid.UUID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}
