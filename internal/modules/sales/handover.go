package sales

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/handover"
	"github.com/bengobox/maskani-api/internal/ent/meter"
	"github.com/bengobox/maskani-api/internal/ent/meterreading"
	"github.com/bengobox/maskani-api/internal/ent/salecontract"
	"github.com/bengobox/maskani-api/internal/ent/unit"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// OwnerLinker makes the buyer the unit's owner from a date (register.LinkParty: the estate account
// opens in their name and the unit is marked lived in). Wired in app.go so sales does not import
// the register module.
type OwnerLinker func(ctx context.Context, unitID, partyID, actor uuid.UUID, from time.Time) error

// SetOwnerLinker wires the owner link used when a handover completes.
func (s *Service) SetOwnerLinker(f OwnerLinker) { s.linkOwner = f }

// Snag is one defect found at handover, followed until fixed.
type Snag struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Location    string   `json:"location,omitempty"`
	Photos      []string `json:"photos,omitempty"`
	Status      string   `json:"status"` // open, fixed
	FixedAt     string   `json:"fixed_at,omitempty"`
}

// HandoverInput updates a handover: when it is booked, the keys and items handed over, the
// checklist and the snags. Lists replace what was there; a completed handover takes snag changes only.
type HandoverInput struct {
	ScheduledAt *time.Time       `json:"scheduled_at"`
	Keys        []map[string]any `json:"keys"`
	Checklist   []map[string]any `json:"checklist"`
	Snags       []Snag           `json:"snags"`
}

// HandoverMeter is a working meter on the unit and the reading a handover reading may not go below.
type HandoverMeter struct {
	ID          uuid.UUID       `json:"id"`
	Serial      string          `json:"serial"`
	LastReading decimal.Decimal `json:"last_reading"`
}

// handoverMeters lists the unit's active meters with their last reading (a unit has one or two).
func (s *Service) handoverMeters(ctx context.Context, unitID uuid.UUID) ([]HandoverMeter, error) {
	ms, err := s.client.Meter.Query().Where(meter.UnitID(unitID), meter.StatusEQ(meter.StatusActive)).
		Order(ent.Asc(meter.FieldSerial)).Limit(20).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]HandoverMeter, 0, len(ms))
	for _, m := range ms {
		last, err := lastReading(ctx, s.client, m)
		if err != nil {
			return nil, err
		}
		out = append(out, HandoverMeter{ID: m.ID, Serial: m.Serial, LastReading: last})
	}
	return out, nil
}

// lastReading is the meter's latest reading that was not rejected, or its initial reading.
func lastReading(ctx context.Context, c *ent.Client, m *ent.Meter) (decimal.Decimal, error) {
	last, err := c.MeterReading.Query().Where(meterreading.MeterID(m.ID), meterreading.StatusNEQ(meterreading.StatusRejected)).
		Order(ent.Desc(meterreading.FieldReadAt)).First(ctx)
	switch {
	case err == nil:
		return last.Reading, nil
	case ent.IsNotFound(err):
		return m.InitialReading, nil
	}
	return decimal.Zero, err
}

// HandoverReading is a meter's reading at handover: where the new owner's water bill starts.
type HandoverReading struct {
	MeterID uuid.UUID `json:"meter_id"`
	Reading float64   `json:"reading"`
}

// StartHandover books the handover of a fully paid unit (one per contract; asking again returns it).
func (s *Service) StartHandover(ctx context.Context, contractID uuid.UUID, scheduledAt *time.Time) (*ent.Handover, error) {
	sc, err := s.client.SaleContract.Get(ctx, contractID)
	if err != nil {
		return nil, err
	}
	if h, err := s.client.Handover.Query().Where(handover.ContractID(sc.ID)).Only(ctx); err == nil {
		return h, nil
	} else if !ent.IsNotFound(err) {
		return nil, err
	}
	if sc.Status != salecontract.StatusFullyPaid {
		return nil, httpx.Conflict("a unit is handed over once its contract is fully paid")
	}
	c := s.client.Handover.Create().SetContractID(sc.ID).SetUnitID(sc.UnitID).
		SetKeys([]map[string]any{}).SetChecklist(defaultChecklist()).SetSnagItems([]map[string]any{})
	if scheduledAt != nil {
		c.SetScheduledAt(*scheduledAt)
	}
	return c.Save(ctx)
}

// defaultChecklist is the walk-through every handover starts with; the officer can change it.
func defaultChecklist() []map[string]any {
	items := []string{"Doors, locks and keys", "Windows and glazing", "Plumbing and taps", "Electrical points and lights",
		"Walls, floors and ceilings", "Kitchen fittings", "Bathroom fittings", "Meter seals intact"}
	out := make([]map[string]any, len(items))
	for i, it := range items {
		out[i] = map[string]any{"item": it, "ok": false}
	}
	return out
}

// UpdateHandover saves the booking, keys, checklist and snags. New snags get an id; a snag marked
// fixed gets its date.
func (s *Service) UpdateHandover(ctx context.Context, id uuid.UUID, in HandoverInput) (*ent.Handover, error) {
	h, err := s.client.Handover.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if h.Status == handover.StatusCancelled {
		return nil, httpx.Conflict("this handover was cancelled")
	}
	done := h.Status == handover.StatusCompleted
	u := h.Update()
	if !done {
		if in.ScheduledAt != nil {
			u.SetScheduledAt(*in.ScheduledAt)
		}
		if in.Keys != nil {
			u.SetKeys(in.Keys)
		}
		if in.Checklist != nil {
			u.SetChecklist(in.Checklist)
		}
		if in.Keys != nil || in.Checklist != nil || in.Snags != nil {
			u.SetStatus(handover.StatusInProgress)
		}
	} else if in.ScheduledAt != nil || in.Keys != nil || in.Checklist != nil {
		return nil, httpx.Conflict("the handover is complete; only snags can still change")
	}
	if in.Snags != nil {
		snags, err := normaliseSnags(in.Snags)
		if err != nil {
			return nil, err
		}
		u.SetSnagItems(snags)
	}
	return u.Save(ctx)
}

func normaliseSnags(in []Snag) ([]map[string]any, error) {
	if len(in) > 200 {
		return nil, httpx.Invalid("at most 200 snags on one handover")
	}
	out := make([]map[string]any, 0, len(in))
	for _, sn := range in {
		sn.Description = strings.TrimSpace(sn.Description)
		if sn.Description == "" || len(sn.Description) > 500 {
			return nil, httpx.Invalid("describe each snag (up to 500 characters)")
		}
		if len(sn.Photos) > 6 {
			return nil, httpx.Invalid("up to 6 photos per snag")
		}
		if sn.ID == "" {
			sn.ID = uuid.NewString()
		}
		switch sn.Status {
		case "", "open":
			sn.Status, sn.FixedAt = "open", ""
		case "fixed":
			if sn.FixedAt == "" {
				sn.FixedAt = time.Now().Format(time.RFC3339)
			}
		default:
			return nil, httpx.Invalid("a snag is open or fixed")
		}
		m := map[string]any{"id": sn.ID, "description": sn.Description, "status": sn.Status}
		if sn.Location != "" {
			m["location"] = sn.Location
		}
		if len(sn.Photos) > 0 {
			m["photos"] = sn.Photos
		}
		if sn.FixedAt != "" {
			m["fixed_at"] = sn.FixedAt
		}
		out = append(out, m)
	}
	return out, nil
}

// CompleteHandover hands the unit over on a date: meter readings at handover become where the
// owner's water bill starts, the unit and contract move to handed over, the buyer becomes the owner
// (estate account in their name) and estate billing starts from that day, prorated by the run.
// Open snags stay on the handover to be fixed.
func (s *Service) CompleteHandover(ctx context.Context, id, by uuid.UUID, at time.Time, readings []HandoverReading) (*ent.Handover, error) {
	h, err := s.client.Handover.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if h.Status == handover.StatusCompleted || h.Status == handover.StatusCancelled {
		return nil, httpx.Conflict("this handover is already closed")
	}
	if len(h.Keys) == 0 {
		return nil, httpx.Invalid("record the keys and items handed over first")
	}
	if at.IsZero() {
		at = time.Now()
	}
	if at.After(time.Now().Add(time.Hour)) {
		return nil, httpx.Invalid("a handover cannot be completed in the future")
	}
	sc, err := s.client.SaleContract.Get(ctx, h.ContractID)
	if err != nil {
		return nil, err
	}
	u, err := s.client.Unit.Get(ctx, h.UnitID)
	if err != nil {
		return nil, err
	}
	period := at.In(s.loc).Format("2006-01")
	rows := []map[string]any{}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*ent.Handover, error) {
		_ = tx.Rollback()
		return nil, err
	}
	for _, rd := range readings {
		m, err := tx.Meter.Query().Where(meter.ID(rd.MeterID), meter.UnitID(u.ID)).Only(ctx)
		if err != nil {
			return fail(httpx.Invalid("a reading names a meter that is not on this unit"))
		}
		reading := decimal.NewFromFloat(rd.Reading)
		prev, err := lastReading(ctx, tx.Client(), m)
		if err != nil {
			return fail(err)
		}
		if reading.LessThan(prev) {
			return fail(httpx.Invalid("a handover reading is below the meter's last reading " + prev.StringFixed(0)))
		}
		// A handover reading is not billed itself; the next round reading measures from it.
		if err := tx.MeterReading.Create().SetMeterID(m.ID).SetUnitID(u.ID).SetPeriod(period).SetReading(reading).
			SetPreviousReading(prev).SetConsumption(decimal.Zero).SetSource(meterreading.SourceHandover).
			SetStatus(meterreading.StatusAccepted).SetReadAt(at).SetReadBy(by).SetNotes("Reading at handover").Exec(ctx); err != nil {
			return fail(err)
		}
		rows = append(rows, map[string]any{"meter_id": m.ID.String(), "serial": m.Serial, "reading": reading.String()})
	}
	h, err = tx.Handover.UpdateOneID(h.ID).SetStatus(handover.StatusCompleted).SetCompletedAt(at).SetReadings(rows).Save(ctx)
	if err != nil {
		return fail(err)
	}
	if err := tx.Unit.UpdateOneID(u.ID).SetHandedOverAt(at).SetSaleStatus(unit.SaleStatusHandedOver).Exec(ctx); err != nil {
		return fail(err)
	}
	if err := tx.SaleContract.UpdateOneID(sc.ID).SetStatus(salecontract.StatusHandedOver).Exec(ctx); err != nil {
		return fail(err)
	}
	buyer, err := tx.Party.Get(ctx, sc.PrimaryBuyerID)
	if err != nil {
		return fail(err)
	}
	open := 0
	for _, sn := range h.SnagItems {
		if sn["status"] != "fixed" {
			open++
		}
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	if err := events.Publish(ctx, tx.OutboxEvent, tenantID, h.ID.String(), events.UnitHandedOver, map[string]any{
		"unit_id": u.ID, "unit_code": u.Code, "contract_id": sc.ID, "contract_number": sc.ContractNumber,
		"name": buyer.DisplayName, "phone": buyer.Phone, "email": buyer.Email, "handed_over_on": at.In(s.loc).Format("2006-01-02"),
		"open_snags": open}); err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if s.linkOwner != nil {
		if err := s.linkOwner(ctx, u.ID, buyer.ID, by, at); err != nil {
			s.log.Warn("owner link after handover", zap.Error(err))
		}
	}
	return h, nil
}

// HandoverOf returns a contract's handover, or nil when none is booked.
func (s *Service) HandoverOf(ctx context.Context, contractID uuid.UUID) (*ent.Handover, error) {
	h, err := s.client.Handover.Query().Where(handover.ContractID(contractID)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	return h, err
}

// HandoverContract returns the contract of a handover (for scope checks).
func (s *Service) HandoverContract(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	h, err := s.client.Handover.Get(ctx, id)
	if err != nil {
		return uuid.Nil, err
	}
	return h.ContractID, nil
}
