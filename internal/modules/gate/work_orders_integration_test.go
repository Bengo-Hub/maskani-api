package gate_test

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
	"github.com/bengobox/maskani-api/internal/ent/workorder"
	"github.com/bengobox/maskani-api/internal/modules/gate"
	"github.com/bengobox/maskani-api/internal/modules/sequence"
	"github.com/bengobox/maskani-api/internal/modules/works"
	"github.com/bengobox/maskani-api/internal/platform/database"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
	"github.com/bengobox/maskani-api/internal/shared/page"
	"github.com/bengobox/maskani-api/internal/shared/secure"
)

// TestWorkOrdersBookedAtGateOnPostgres: a provider let in for a unit books a work order for that
// unit (source gate, responded at arrival, provider kept, no quote or cost needed); one let in for
// the estate books an estate job with a known vendor; a contractor pass already tied to a work
// order links to it; replaying the tablet's queue books nothing twice. Needs GATE_TEST_POSTGRES_URL.
func TestWorkOrdersBookedAtGateOnPostgres(t *testing.T) {
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
	box, err := secure.NewBox("test-secret-key-for-gate-work-orders-01")
	must(err)
	seq := sequence.NewAllocator(client, time.UTC)
	g := gate.NewService(client, box, seq, zap.NewNop())
	w := works.NewService(client, seq, zap.NewNop())
	g.SetWorkOrderBooker(func(ctx context.Context, b gate.Booking) (uuid.UUID, error) {
		wo, err := w.BookFromGate(ctx, works.GateBooking{PropertyID: b.PropertyID, UnitID: b.UnitID, Area: b.WorkOrder.Area,
			Category: b.WorkOrder.Category, Title: b.WorkOrder.Title, Description: b.WorkOrder.Description, Priority: b.WorkOrder.Priority,
			VendorID: b.WorkOrder.VendorID, ProviderName: b.ProviderName, ProviderPhone: b.ProviderPhone, GateEventID: b.GateEventID, DeviceID: b.DeviceID})
		if err != nil {
			return uuid.Nil, err
		}
		return wo.ID, nil
	})
	prop, err := client.Property.Create().SetCode("MV").SetName("Mvumo").Save(ctx)
	must(err)
	u, err := client.Unit.Create().SetPropertyID(prop.ID).SetCode("B07").Save(ctx)
	must(err)
	vendor, err := client.Vendor.Create().SetName("Pump Masters").Save(ctx)
	must(err)
	dev, err := client.GateDevice.Create().SetPropertyID(prop.ID).SetName("Tablet").SetDeviceKeyHash(uuid.NewString()).Save(ctx)
	must(err)
	existing, err := w.Create(ctx, works.Actor{Kind: "staff", UserID: uuid.New()}, works.RequestInput{PropertyID: prop.ID, Category: "plumbing", Title: "Leak in B07"})
	must(err)
	pass, err := client.VisitorPass.Create().SetPropertyID(prop.ID).SetVisitorName("Plumber").SetCodeHash(uuid.NewString()).
		SetPassType("contractor").SetValidFrom(time.Now().Add(-time.Hour)).SetValidTo(time.Now().Add(time.Hour)).SetWorkOrderID(existing.ID).Save(ctx)
	must(err)

	batch := []gate.EventInput{
		{ClientEventID: "e1", Kind: "entry", VisitorName: "John Fundi", VisitorPhone: "0712000555", HostUnitID: &u.ID,
			WorkOrder: &gate.WorkOrderAtGate{Category: "electrical", Description: "Sockets in the kitchen"}},
		{ClientEventID: "e2", Kind: "entry", VisitorName: "Pump Masters crew",
			WorkOrder: &gate.WorkOrderAtGate{Title: "Borehole pump service", Category: "water_supply", Area: "Pump house", VendorID: &vendor.ID}},
		{ClientEventID: "e3", Kind: "entry", PassID: &pass.ID},
		{ClientEventID: "e4", Kind: "entry", VisitorName: "Guest"},
	}
	n, err := g.Record(ctx, dev, batch)
	must(err)
	if n != 4 {
		t.Fatalf("stored %d", n)
	}
	gateWOs, err := client.WorkOrder.Query().Where(workorder.SourceEQ(workorder.SourceGate)).All(ctx)
	must(err)
	if len(gateWOs) != 2 {
		t.Fatalf("gate work orders: %d", len(gateWOs))
	}
	for _, wo := range gateWOs {
		if wo.RespondedAt == nil || wo.QuoteAmount != nil || wo.Metadata["booked_at_gate"] != true {
			t.Fatalf("booked at gate: %+v", wo.Metadata)
		}
		switch {
		case wo.UnitID != nil:
			prov, _ := wo.Metadata["provider"].(map[string]any)
			if *wo.UnitID != u.ID || wo.Category != "electrical" || prov["name"] != "John Fundi" || wo.Title != "Visit by John Fundi" {
				t.Fatalf("unit job: %s %s %v", wo.Category, wo.Title, prov)
			}
		default:
			if wo.Area != "Pump house" || wo.VendorID == nil || *wo.VendorID != vendor.ID || wo.AssigneeKind != workorder.AssigneeKindVendor {
				t.Fatalf("estate job: %s %v", wo.Area, wo.VendorID)
			}
		}
	}
	evs, err := client.GateEvent.Query().All(ctx)
	must(err)
	for _, ev := range evs {
		id, _ := ev.Metadata["work_order_id"].(string)
		switch ev.ClientEventID {
		case "e3":
			if id != existing.ID.String() {
				t.Fatalf("pass entry links its work order, got %q", id)
			}
		case "e4":
			if id != "" {
				t.Fatal("a guest books nothing")
			}
		default:
			if id == "" || ev.Metadata["work_order_request"] != nil {
				t.Fatalf("entry %s: %v", ev.ClientEventID, ev.Metadata)
			}
		}
	}
	if _, err := g.Record(ctx, dev, batch); err != nil {
		t.Fatal(err)
	}
	if n, _ := client.WorkOrder.Query().Where(workorder.SourceEQ(workorder.SourceGate)).Count(ctx); n != 2 {
		t.Fatalf("a replay books nothing twice: %d", n)
	}
	// A walk-in asked of the host books the job only once someone lets the provider in.
	asks := []gate.EventInput{
		{ClientEventID: "w1", Kind: "walk_in_request", VisitorName: "Gas refill", HostUnitID: &u.ID, WorkOrder: &gate.WorkOrderAtGate{Category: "general"}},
		{ClientEventID: "w2", Kind: "walk_in_request", VisitorName: "Painter", HostUnitID: &u.ID, WorkOrder: &gate.WorkOrderAtGate{Title: "Repaint door"}},
		{ClientEventID: "w3", Kind: "walk_in_request", VisitorName: "Hawker", HostUnitID: &u.ID, WorkOrder: &gate.WorkOrderAtGate{}},
	}
	_, err = g.Record(ctx, dev, asks)
	must(err)
	if n, _ := client.WorkOrder.Query().Where(workorder.SourceEQ(workorder.SourceGate)).Count(ctx); n != 2 {
		t.Fatalf("a pending walk-in books nothing: %d", n)
	}
	w1, err := g.DeviceEvent(ctx, dev.ID, "w1")
	must(err)
	w2, err := g.DeviceEvent(ctx, dev.ID, "w2")
	must(err)
	w3, err := g.DeviceEvent(ctx, dev.ID, "w3")
	must(err)
	_, err = g.Decide(ctx, w1.ID, true)
	must(err)
	_, err = g.ResolveWalkIn(ctx, dev, w2.ID, true, "")
	must(err)
	_, err = g.ResolveWalkIn(ctx, dev, w2.ID, true, "")
	must(err)
	_, err = g.Decide(ctx, w3.ID, false)
	must(err)
	if n, _ := client.WorkOrder.Query().Where(workorder.SourceEQ(workorder.SourceGate)).Count(ctx); n != 4 {
		t.Fatalf("approved walk-ins book once each, a declined one never: %d", n)
	}
	listed, err := w.List(ctx, works.Filter{Source: "gate", AllProperties: true}, page.Params{Limit: 10})
	must(err)
	if len(listed.Data) != 4 {
		t.Fatalf("office lists gate bookings: %d", len(listed.Data))
	}
	if _, err := w.List(ctx, works.Filter{Source: "walk_in", AllProperties: true}, page.Params{Limit: 10}); err == nil {
		t.Fatal("an unknown source is refused")
	}
	sync, err := g.Sync(ctx, dev)
	must(err)
	if len(sync.Vendors) != 1 || sync.Vendors[0].Name != "Pump Masters" {
		t.Fatalf("sync vendors: %+v", sync.Vendors)
	}
}
