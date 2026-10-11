package register

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
	"github.com/bengobox/maskani-api/internal/ent/unitparty"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/platform/database"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// TestOccupancyOnPostgres: an occupant ending today loses their passes at once and a final reading
// is asked for; an owner with a future end date stays until the job runs after it, and then the
// account moves to the remaining owner. Needs REGISTER_TEST_POSTGRES_URL (a scratch database).
func TestOccupancyOnPostgres(t *testing.T) {
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
	tid := uuid.New()
	ctx := tenantguard.With(context.Background(), tid)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	s := NewService(client, nil, nil, nil, zap.NewNop())
	p, err := client.Property.Create().SetCode("SV").SetName("Mvumo").Save(ctx)
	must(err)
	u, err := client.Unit.Create().SetPropertyID(p.ID).SetCode("A01").Save(ctx)
	must(err)
	_, err = client.Meter.Create().SetUnitID(u.ID).SetPropertyID(p.ID).SetSerial("M-1").Save(ctx)
	must(err)
	party := func(name, phone string) *ent.Party {
		x, err := client.Party.Create().SetDisplayName(name).SetPhone(phone).Save(ctx)
		must(err)
		return x
	}
	owner, coOwner, tenant := party("Owner", "254700000001"), party("Co Owner", "254700000002"), party("Tenant", "254700000003")
	start := time.Now().AddDate(-1, 0, 0)
	link := func(pt *ent.Party, role string, primary bool) *ent.UnitParty {
		l, err := client.UnitParty.Create().SetUnitID(u.ID).SetPartyID(pt.ID).SetRole(unitparty.Role(role)).SetIsPrimary(primary).SetStartDate(start).Save(ctx)
		must(err)
		return l
	}
	ownerLink, _, tenantLink := link(owner, "owner", true), link(coOwner, "joint_owner", false), link(tenant, "occupant", false)
	f, err := client.Fund.Create().SetCode("estate").SetName("Estate").Save(ctx)
	must(err)
	acc, err := client.UnitAccount.Create().SetUnitID(u.ID).SetFundID(f.ID).SetAccountRef("A01").SetPrimaryPartyID(owner.ID).
		SetCustomerName("Owner").SetCustomerPhone("254700000001").Save(ctx)
	must(err)
	pass, err := client.VisitorPass.Create().SetPropertyID(p.ID).SetUnitID(u.ID).SetHostPartyID(tenant.ID).SetVisitorName("Guest").
		SetCodeHash("h").SetValidFrom(time.Now()).SetValidTo(time.Now().Add(48 * time.Hour)).Save(ctx)
	must(err)

	_, err = s.EndLink(ctx, tenantLink.ID, time.Now().Add(-time.Minute))
	must(err)
	if got, _ := client.VisitorPass.Get(ctx, pass.ID); got.Status != "cancelled" {
		t.Fatalf("the tenant's pass should be cancelled, got %s", got.Status)
	}
	if n, _ := client.OutboxEvent.Query().Where(outboxevent.EventType(events.FinalReadingNeeded)).Count(ctx); n != 1 {
		t.Fatalf("one final reading request, got %d", n)
	}

	_, err = s.EndLink(ctx, ownerLink.ID, time.Now().Add(24*time.Hour))
	must(err)
	if got, _ := client.UnitParty.Get(ctx, ownerLink.ID); got.Status != "active" {
		t.Fatal("a future end date keeps the link active")
	}
	n, err := s.CloseLinks(context.Background(), []uuid.UUID{tid}, time.Now().Add(25*time.Hour), 100)
	must(err)
	if n != 1 {
		t.Fatalf("the job should end one link, ended %d", n)
	}
	got, _ := client.UnitAccount.Get(ctx, acc.ID)
	if got.PrimaryPartyID == nil || *got.PrimaryPartyID != coOwner.ID || got.CustomerName != "Co Owner" {
		t.Fatalf("the account should move to the co-owner: %+v", got.CustomerName)
	}
	if n, _ := s.CloseLinks(context.Background(), []uuid.UUID{tid}, time.Now().Add(25*time.Hour), 100); n != 0 {
		t.Fatal("a second run ends nothing")
	}

	// Transfer: refused while an account owes; once clear, the buyer becomes owner and the account follows.
	_, err = client.UnitAccount.UpdateOneID(acc.ID).SetBalance(decimal.NewFromInt(500)).Save(ctx)
	must(err)
	buyer := party("Buyer", "254700000009")
	if _, err := s.Transfer(ctx, u.ID, uuid.New(), "demo", "", TransferInput{NewOwnerID: buyer.ID}); err == nil {
		t.Fatal("a unit that owes should not transfer")
	}
	_, err = client.UnitAccount.UpdateOneID(acc.ID).SetBalance(decimal.Zero).Save(ctx)
	must(err)
	res, err := s.Transfer(ctx, u.ID, uuid.New(), "demo", "", TransferInput{NewOwnerID: buyer.ID, Reading: ptrF(150)})
	must(err)
	if res.EndedLinks != 1 || res.Apportion.HandoverUse == nil || res.Apportion.DaysInPeriod == 0 {
		t.Fatalf("transfer: %+v", res)
	}
	got, _ = client.UnitAccount.Get(ctx, acc.ID)
	if got.PrimaryPartyID == nil || *got.PrimaryPartyID != buyer.ID {
		t.Fatal("the account should follow the new owner")
	}
}

func ptrF(v float64) *float64 { return &v }
