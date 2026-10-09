package gate

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
	"github.com/bengobox/maskani-api/internal/ent/gateevent"
	_ "github.com/bengobox/maskani-api/internal/ent/runtime"
	"github.com/bengobox/maskani-api/internal/platform/database"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
	"github.com/bengobox/maskani-api/internal/shared/secure"
)

// TestGateFlowsOnPostgres runs the visitor registry, exits and walk-in resolution against a real
// Postgres. Needs GATE_TEST_POSTGRES_URL pointing at a scratch database (its schema is created).
func TestGateFlowsOnPostgres(t *testing.T) {
	dsn := os.Getenv("GATE_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("GATE_TEST_POSTGRES_URL not set")
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
	box, err := secure.NewBox("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(client, box, nil, zap.NewNop())
	tctx := tenantguard.With(ctx, uuid.New())
	prop := uuid.New()
	dev, err := client.GateDevice.Create().SetPropertyID(prop).SetName("Tablet").SetDeviceKeyHash(uuid.NewString()).Save(tctx)
	if err != nil {
		t.Fatal(err)
	}

	// A walk-in by phone creates the visitor; a second by the same phone (other spelling) matches.
	n, err := s.Record(tctx, dev, []EventInput{{ClientEventID: "e1", Kind: "entry", VisitorName: "Jane Wanjiku", VisitorPhone: "0712 345 678", VehiclePlate: "kda 123a"}})
	if err != nil || n != 1 {
		t.Fatalf("record entry: %d %v", n, err)
	}
	if _, err := s.Record(tctx, dev, []EventInput{{ClientEventID: "e2", Kind: "entry", VisitorName: "Jane", VisitorPhone: "+254712345678"}}); err != nil {
		t.Fatal(err)
	}
	vs, err := client.Visitor.Query().All(tctx)
	if err != nil || len(vs) != 1 || vs[0].Visits != 2 || vs[0].VehiclePlate != "KDA123A" {
		t.Fatalf("one returning visitor with two visits expected, got %+v %v", vs, err)
	}
	m, err := s.LookupVisitors(tctx, prop, "KDA1")
	if err != nil || len(m) != 1 {
		t.Fatalf("plate prefix lookup: %+v %v", m, err)
	}
	if m, err = s.LookupVisitors(tctx, prop, "0712345"); err != nil || len(m) != 1 {
		t.Fatalf("phone lookup: %+v %v", m, err)
	}

	// Two people inside; an exit by plate closes the first; repeated exits for it record nothing.
	inside, err := s.Inside(tctx, prop)
	if err != nil || len(inside) != 2 {
		t.Fatalf("inside: %+v %v", inside, err)
	}
	if _, err := s.Record(tctx, dev, []EventInput{{ClientEventID: "x1", Kind: "exit", VehiclePlate: "KDA 123A"}}); err != nil {
		t.Fatal(err)
	}
	first := inside[1].EventID // newest first, so [1] is e1
	n, _ = s.Record(tctx, dev, []EventInput{{ClientEventID: "x2", Kind: "exit", EntryEventID: &first}})
	if n != 0 {
		t.Fatalf("a second exit for the same entry was recorded")
	}
	if n, _ = s.Record(tctx, dev, []EventInput{{ClientEventID: "x3", Kind: "exit"}}); n != 0 {
		t.Fatalf("an exit naming nobody was recorded")
	}
	if inside, _ = s.Inside(tctx, prop); len(inside) != 1 {
		t.Fatalf("one person should still be inside, got %d", len(inside))
	}
	exits, _ := client.GateEvent.Query().Where(gateevent.KindEQ(gateevent.KindExit)).All(tctx)
	if len(exits) != 1 || exits[0].EntryEventID == nil || exits[0].VisitorName != "Jane Wanjiku" {
		t.Fatalf("one exit naming the leaver expected, got %+v", exits)
	}

	// A refused walk-in stays one row; letting one in puts them inside.
	if _, err := s.Record(tctx, dev, []EventInput{{ClientEventID: "w1", Kind: "walk_in_request", VisitorName: "Courier", OccurredAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	w, _ := s.DeviceEvent(tctx, dev.ID, "w1")
	if _, err := s.ResolveWalkIn(tctx, dev, w.ID, false, ""); err != nil {
		t.Fatal(err)
	}
	if c, _ := client.GateEvent.Query().Where(gateevent.VisitorName("Courier")).Count(tctx); c != 1 {
		t.Fatalf("a refused walk-in made %d rows", c)
	}
	if _, err := s.Record(tctx, dev, []EventInput{{ClientEventID: "w2", Kind: "walk_in_request", VisitorName: "Plumber"}}); err != nil {
		t.Fatal(err)
	}
	w2, _ := s.DeviceEvent(tctx, dev.ID, "w2")
	if _, err := s.ResolveWalkIn(tctx, dev, w2.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	if inside, _ = s.Inside(tctx, prop); len(inside) != 2 || !inside[0].WalkIn {
		t.Fatalf("the admitted walk-in should be inside: %+v", inside)
	}
}
