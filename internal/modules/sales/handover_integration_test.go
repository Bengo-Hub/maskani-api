package sales

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
	"github.com/bengobox/maskani-api/internal/ent/meterreading"
	"github.com/bengobox/maskani-api/internal/ent/outboxevent"
	_ "github.com/bengobox/maskani-api/internal/ent/runtime"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/modules/treasury"
	"github.com/bengobox/maskani-api/internal/modules/utilities"
	"github.com/bengobox/maskani-api/internal/platform/database"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// TestHandoverAndTitleOnPostgres: only a fully paid sale is handed over; completion needs the keys,
// records the meter reading the owner's water starts from (the month's round reading measures from
// it), moves unit and contract to handed over, makes the buyer the owner and publishes the event;
// snags can still be fixed after. Title stages follow the estate's catalogue, and the last one done
// titles the sale. Needs SALES_TEST_POSTGRES_URL (a scratch database).
func TestHandoverAndTitleOnPostgres(t *testing.T) {
	dsn := os.Getenv("SALES_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("SALES_TEST_POSTGRES_URL not set")
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
	loc := time.FixedZone("EAT", 3*3600)
	s := NewService(client, treasury.NewClient("", "", zap.NewNop()), nil, nil, loc, zap.NewNop())
	var linked []uuid.UUID
	s.SetOwnerLinker(func(_ context.Context, unitID, partyID, _ uuid.UUID, _ time.Time) error {
		linked = append(linked, unitID, partyID)
		return nil
	})
	s.SetCatalogue(func(context.Context, uuid.UUID, string) ([]*ent.CatalogEntry, error) {
		return []*ent.CatalogEntry{{Code: "stamp_duty", Name: "Stamp duty paid"}, {Code: "title_released", Name: "Title released to owner"}}, nil
	})
	staff := uuid.New()
	d := func(n int64) decimal.Decimal { return decimal.NewFromInt(n) }

	prop, err := client.Property.Create().SetCode("MV").SetName("Mvumo").Save(ctx)
	must(err)
	u, err := client.Unit.Create().SetPropertyID(prop.ID).SetCode("C03").SetSaleStatus("fully_paid").Save(ctx)
	must(err)
	buyer, err := client.Party.Create().SetDisplayName("Owner To Be").Save(ctx)
	must(err)
	sc, err := client.SaleContract.Create().SetContractNumber("SC-0002").SetPropertyID(prop.ID).SetUnitID(u.ID).
		SetPrimaryBuyerID(buyer.ID).SetPrice(d(500_000)).SetNetPrice(d(500_000)).SetStatus("active").Save(ctx)
	must(err)
	m, err := client.Meter.Create().SetPropertyID(prop.ID).SetUnitID(u.ID).SetSerial("WM-C03").SetInitialReading(d(10)).Save(ctx)
	must(err)

	if _, err := s.StartHandover(ctx, sc.ID, nil); err == nil {
		t.Fatal("an unpaid sale is not handed over")
	}
	must(client.SaleContract.UpdateOneID(sc.ID).SetStatus("fully_paid").Exec(ctx))
	ho, err := s.StartHandover(ctx, sc.ID, nil)
	must(err)
	if again, err := s.StartHandover(ctx, sc.ID, nil); err != nil || again.ID != ho.ID {
		t.Fatal("one handover per contract")
	}
	if len(ho.Checklist) == 0 {
		t.Fatal("a handover starts with the walk-through checklist")
	}
	if _, err := s.CompleteHandover(ctx, ho.ID, staff, time.Time{}, nil); err == nil {
		t.Fatal("no keys recorded, no handover")
	}
	ho, err = s.UpdateHandover(ctx, ho.ID, HandoverInput{Keys: []map[string]any{{"item": "Front door", "count": 2}},
		Snags: []Snag{{Description: "Cracked tile in the kitchen", Location: "Kitchen"}}})
	must(err)
	if ho.Status != "in_progress" || len(ho.SnagItems) != 1 || ho.SnagItems[0]["id"] == "" {
		t.Fatalf("update: %s %v", ho.Status, ho.SnagItems)
	}
	if _, err := s.CompleteHandover(ctx, ho.ID, staff, time.Time{}, []HandoverReading{{MeterID: m.ID, Reading: 5}}); err == nil {
		t.Fatal("a reading below the meter's last is refused")
	}
	at := time.Now().In(loc)
	ho, err = s.CompleteHandover(ctx, ho.ID, staff, at, []HandoverReading{{MeterID: m.ID, Reading: 25}})
	must(err)
	if ho.Status != "completed" || len(ho.Readings) != 1 {
		t.Fatalf("complete: %s %v", ho.Status, ho.Readings)
	}
	gotU, _ := client.Unit.Get(ctx, u.ID)
	gotC, _ := client.SaleContract.Get(ctx, sc.ID)
	if gotU.HandedOverAt == nil || gotU.SaleStatus != "handed_over" || gotC.Status != "handed_over" {
		t.Fatalf("unit %v %s contract %s", gotU.HandedOverAt, gotU.SaleStatus, gotC.Status)
	}
	if len(linked) != 2 || linked[0] != u.ID || linked[1] != buyer.ID {
		t.Fatalf("the buyer becomes the owner: %v", linked)
	}
	if n, _ := client.OutboxEvent.Query().Where(outboxevent.EventType(events.UnitHandedOver)).Count(ctx); n != 1 {
		t.Fatalf("handed over events: %d", n)
	}
	// The month's round reading measures from the handover reading, not from before it.
	round, err := utilities.NewService(client, zap.NewNop()).Record(ctx, m.ID, staff, utilities.ReadingInput{Period: at.Format("2006-01"), Reading: 31})
	must(err)
	if !round.PreviousReading.Equal(d(25)) || !round.Consumption.Equal(d(6)) {
		t.Fatalf("round reading: previous %s consumption %s", round.PreviousReading, round.Consumption)
	}
	if n, _ := client.MeterReading.Query().Where(meterreading.SourceEQ(meterreading.SourceHandover)).Count(ctx); n != 1 {
		t.Fatal("one handover reading")
	}
	if _, err := s.UpdateHandover(ctx, ho.ID, HandoverInput{Keys: []map[string]any{}}); err == nil {
		t.Fatal("keys cannot change after completion")
	}
	snag := ho.SnagItems[0]
	ho, err = s.UpdateHandover(ctx, ho.ID, HandoverInput{Snags: []Snag{{ID: snag["id"].(string), Description: "Cracked tile in the kitchen", Status: "fixed"}}})
	must(err)
	if ho.SnagItems[0]["status"] != "fixed" || ho.SnagItems[0]["fixed_at"] == nil {
		t.Fatalf("snag fixed after handover: %v", ho.SnagItems[0])
	}

	// Title.
	if _, err := s.SetTitleStage(ctx, sc.ID, "title_released", TitleStageInput{Status: "done"}); err == nil {
		t.Fatal("a done stage needs its date")
	}
	if _, err := s.SetTitleStage(ctx, sc.ID, "not_a_stage", TitleStageInput{Status: "pending"}); err == nil {
		t.Fatal("only the estate's stages")
	}
	stages, err := s.SetTitleStage(ctx, sc.ID, "stamp_duty", TitleStageInput{Status: "done", StageDate: &at, Reference: "SD-123"})
	must(err)
	if len(stages) != 2 || stages[0].Status != "done" || stages[0].Reference != "SD-123" || stages[1].Status != "pending" {
		t.Fatalf("stages: %+v", stages)
	}
	_, err = s.SetTitleStage(ctx, sc.ID, "title_released", TitleStageInput{Status: "done", StageDate: &at})
	must(err)
	gotU, _ = client.Unit.Get(ctx, u.ID)
	gotC, _ = client.SaleContract.Get(ctx, sc.ID)
	if gotU.SaleStatus != "titled" || gotC.Status != "titled" {
		t.Fatalf("titled: unit %s contract %s", gotU.SaleStatus, gotC.Status)
	}
	view, err := s.GetContract(ctx, sc.ID)
	must(err)
	if view.Handover == nil || view.Handover.ID != ho.ID {
		t.Fatal("the contract view carries its handover")
	}
}
