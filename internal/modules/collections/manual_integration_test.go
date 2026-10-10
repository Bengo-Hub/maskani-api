package collections

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
	_ "github.com/bengobox/maskani-api/internal/ent/runtime"
	"github.com/bengobox/maskani-api/internal/platform/database"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// TestManualPaymentsOnPostgres covers the review rules that run before treasury is called: one
// slip once, residents limited to bank and cheque, no self-approval, and a rejected slip that can
// be corrected and submitted again. Needs COLLECTIONS_TEST_POSTGRES_URL (a scratch database).
func TestManualPaymentsOnPostgres(t *testing.T) {
	dsn := os.Getenv("COLLECTIONS_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("COLLECTIONS_TEST_POSTGRES_URL not set")
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
	tctx := tenantguard.With(ctx, uuid.New())
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
	acc, err := client.UnitAccount.Create().SetUnitID(u.ID).SetFundID(f.ID).SetAccountRef("A01").Save(tctx)
	must(err)
	s := NewService(client, nil, nil, zap.NewNop())
	clerk := Submitter{UserID: uuid.New(), Name: "Clerk"}
	in := ManualInput{Amount: decimal.NewFromInt(300000), Method: "bank_transfer", Reference: "ft-001"}

	mp, err := s.SubmitManual(tctx, acc.ID, clerk, in)
	must(err)
	if mp.Reference != "FT-001" || mp.Status != "pending" {
		t.Fatalf("submitted: %+v", mp)
	}
	if _, err := s.SubmitManual(tctx, acc.ID, clerk, in); err == nil {
		t.Fatal("the same slip twice should be refused")
	}
	if _, err := s.SubmitManual(tctx, acc.ID, Submitter{UserID: uuid.New(), Portal: true}, ManualInput{Amount: decimal.NewFromInt(10), Method: "cash", Reference: "X1"}); err == nil {
		t.Fatal("a resident should not record cash")
	}
	if _, err := s.ApproveManual(tctx, mp.ID, Reviewer{UserID: clerk.UserID}, ""); err == nil {
		t.Fatal("the person who recorded it should not approve it")
	}
	if _, err := s.RejectManual(tctx, mp.ID, Reviewer{UserID: uuid.New(), Name: "Manager"}, "slip unreadable"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitManual(tctx, acc.ID, clerk, in); err != nil {
		t.Fatalf("a rejected slip should be submittable again: %v", err)
	}
}
