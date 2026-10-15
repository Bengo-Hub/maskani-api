package works

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
	"github.com/bengobox/maskani-api/internal/ent/outboxevent"
	_ "github.com/bengobox/maskani-api/internal/ent/runtime"
	"github.com/bengobox/maskani-api/internal/platform/database"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// TestVendorContractsOnPostgres: a contract is checked on the way in; the daily job announces a
// contract in its notice period once, renews an auto-renewing one past its end for the same
// length, expires the others, and a terminated contract keeps the reason. Needs
// WORKS_TEST_POSTGRES_URL (a scratch database).
func TestVendorContractsOnPostgres(t *testing.T) {
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
	tenant := uuid.New()
	ctx := tenantguard.With(context.Background(), tenant)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	s := NewService(client, nil, zap.NewNop())
	prop, err := client.Property.Create().SetCode("MV").SetName("Mvumo").Save(ctx)
	must(err)
	v, err := client.Vendor.Create().SetName("Guardforce").Save(ctx)
	must(err)
	day := func(n int) time.Time { return time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, n) }
	ptr := func(t time.Time) *time.Time { return &t }
	base := ContractInput{PropertyID: prop.ID, ServiceCategory: "security", FeeBasis: "fixed_monthly", FeeAmount: decimal.NewFromInt(90000)}

	bad := base
	bad.StartsOn, bad.EndsOn = day(0), ptr(day(-1))
	if _, err := s.CreateContract(ctx, v.ID, bad); err == nil {
		t.Fatal("a contract cannot end before it starts")
	}
	bad = base
	bad.StartsOn, bad.AutoRenew = day(0), true
	if _, err := s.CreateContract(ctx, v.ID, bad); err == nil {
		t.Fatal("auto renewal needs an end date")
	}
	soon := base
	soon.StartsOn, soon.EndsOn, soon.RenewalNoticeDays = day(-300), ptr(day(10)), 30
	cSoon, err := s.CreateContract(ctx, v.ID, soon)
	must(err)
	auto := base
	auto.StartsOn, auto.EndsOn, auto.AutoRenew = day(-365), ptr(day(-1)), true
	cAuto, err := s.CreateContract(ctx, v.ID, auto)
	must(err)
	over := base
	over.StartsOn, over.EndsOn = day(-200), ptr(day(-2))
	cOver, err := s.CreateContract(ctx, v.ID, over)
	must(err)

	alerted, renewed, expired, err := s.RenewContracts(tenantguard.System(ctx), []uuid.UUID{tenant})
	must(err)
	if alerted != 1 || renewed != 1 || expired != 1 {
		t.Fatalf("job: alerted %d renewed %d expired %d", alerted, renewed, expired)
	}
	again, _, _, err := s.RenewContracts(tenantguard.System(ctx), []uuid.UUID{tenant})
	must(err)
	if again != 0 {
		t.Fatal("the renewal notice goes once")
	}
	if n, _ := client.OutboxEvent.Query().Where(outboxevent.EventType(ContractRenewalDue)).Count(ctx); n != 1 {
		t.Fatalf("renewal events: %d", n)
	}
	got, _ := client.VendorContract.Get(ctx, cAuto.ID)
	if got.Status != "active" || !got.EndsOn.After(day(300)) {
		t.Fatalf("auto renewed: %s ends %s", got.Status, got.EndsOn)
	}
	if got, _ := client.VendorContract.Get(ctx, cOver.ID); got.Status != "expired" {
		t.Fatalf("expired: %s", got.Status)
	}
	if _, err := s.TerminateContract(ctx, cSoon.ID, uuid.New(), ""); err == nil {
		t.Fatal("termination needs a reason")
	}
	term, err := s.TerminateContract(ctx, cSoon.ID, uuid.New(), "Guards missing posts")
	must(err)
	if term.Status != "terminated" || term.Metadata["termination"] == nil {
		t.Fatalf("terminated: %s", term.Status)
	}
	detail, err := s.GetVendor(ctx, v.ID, nil, true)
	must(err)
	if len(detail.Contracts) != 3 {
		t.Fatalf("vendor detail contracts: %d", len(detail.Contracts))
	}
}
