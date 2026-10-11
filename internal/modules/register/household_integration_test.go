package register

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
	"github.com/bengobox/maskani-api/internal/shared/secure"
)

// TestHouseholdOnPostgres: a resident adds domestic staff (matched by phone when known), a car, and
// removes them; owners and occupants are never removable from the portal. Needs
// REGISTER_TEST_POSTGRES_URL (a scratch database).
func TestHouseholdOnPostgres(t *testing.T) {
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
	ctx := tenantguard.With(context.Background(), uuid.New())
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	box, err := secure.NewBox("test-secret-key-for-household-0123456789")
	must(err)
	s := NewService(client, nil, nil, box, zap.NewNop())
	p, err := client.Property.Create().SetCode("MV").SetName("Mvumo").Save(ctx)
	must(err)
	u, err := client.Unit.Create().SetPropertyID(p.ID).SetCode("B02").Save(ctx)
	must(err)
	owner, err := client.Party.Create().SetDisplayName("Owner").Save(ctx)
	must(err)
	ownerLink, err := client.UnitParty.Create().SetUnitID(u.ID).SetPartyID(owner.ID).SetRole("owner").SetStartDate(time.Now()).Save(ctx)
	must(err)
	actor := uuid.New()

	if _, err := s.AddHouseholdMember(ctx, u.ID, actor, HouseholdInput{Name: "X", Role: "owner"}); err == nil {
		t.Fatal("only household roles can be added from the portal")
	}
	staff, err := s.AddHouseholdMember(ctx, u.ID, actor, HouseholdInput{Name: "Mary Wanjiku", Phone: "0712000111", Role: "domestic_staff"})
	must(err)
	if _, err := s.AddHouseholdMember(ctx, u.ID, actor, HouseholdInput{Name: "Mary W", Phone: "+254712000111", Role: "domestic_staff"}); err == nil {
		t.Fatal("the same phone is the same person, already on the unit")
	}
	car, err := s.AddVehicle(ctx, u.ID, &owner.ID, "kdq 123a", "Toyota", "Axio", "white")
	must(err)
	hh, err := s.HouseholdOf(ctx, u.ID)
	must(err)
	if len(hh.People) != 1 || len(hh.Vehicles) != 1 || hh.Vehicles[0].Plate != "KDQ123A" {
		t.Fatalf("household: %+v", hh)
	}
	if _, err := s.EndHouseholdLink(ctx, u.ID, ownerLink.ID); err == nil {
		t.Fatal("an owner's link cannot be ended from the portal")
	}
	_, err = s.EndHouseholdLink(ctx, u.ID, staff.ID)
	must(err)
	_, err = s.RemoveVehicle(ctx, u.ID, car.ID)
	must(err)
	hh, err = s.HouseholdOf(ctx, u.ID)
	must(err)
	if len(hh.People) != 0 || len(hh.Vehicles) != 0 {
		t.Fatalf("after removal: %+v", hh)
	}
}
