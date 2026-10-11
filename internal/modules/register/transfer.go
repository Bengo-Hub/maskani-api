package register

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/meter"
	"github.com/bengobox/maskani-api/internal/ent/meterreading"
	"github.com/bengobox/maskani-api/internal/ent/unitaccount"
	"github.com/bengobox/maskani-api/internal/ent/unitparty"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// UnitTransferred records a resale or transfer of a unit to a new owner.
const UnitTransferred = "unit.transferred"

// TransferInput hands a unit from its current owners to a new one (SRDD 16.3).
type TransferInput struct {
	NewOwnerID uuid.UUID `json:"new_owner_id"`
	// Date is when ownership passes; today or earlier ends the old ownership now.
	Date *time.Time `json:"date"`
	// Reading is the meter reading at handover (optional): kept as a move-out reading that the
	// period's bill does not double count, and used to split the month's water between owners.
	Reading *float64 `json:"reading"`
	Invite  bool     `json:"invite"`
	Note    string   `json:"note"`
}

// Apportion splits the transfer month between the outgoing and incoming owner by days, for the
// fixed charges finance settles between them.
type Apportion struct {
	Period       string `json:"period"`
	DaysInPeriod int    `json:"days_in_period"`
	OutgoingDays int    `json:"outgoing_days"`
	IncomingDays int    `json:"incoming_days"`
	// HandoverUse is the water used this period up to handover (from the handover reading).
	HandoverUse *decimal.Decimal `json:"handover_use,omitempty"`
}

// TransferResult is what the transfer did.
type TransferResult struct {
	NewLink    *ent.UnitParty `json:"new_link"`
	EndedLinks int            `json:"ended_links"`
	Apportion  Apportion      `json:"apportion"`
	Invited    bool           `json:"invited"`
}

// Transfer moves a unit to a new owner. Every account on the unit must be clear first (clearance:
// the outgoing owner settles before the unit changes hands). The new owner is linked as primary
// owner first, then the current owners' links end on the date with the usual effects (passes,
// final reading request, accounts moving to the new owner). Returns the month's split by days.
func (s *Service) Transfer(ctx context.Context, unitID, actor uuid.UUID, tenantSlug, portalURL string, in TransferInput) (*TransferResult, error) {
	date := time.Now()
	if in.Date != nil {
		date = *in.Date
	}
	u, err := s.client.Unit.Get(ctx, unitID)
	if err != nil {
		return nil, err
	}
	np, err := s.client.Party.Get(ctx, in.NewOwnerID)
	if err != nil {
		return nil, httpx.Invalid("choose the new owner from the register")
	}
	if n, err := s.client.UnitAccount.Query().Where(unitaccount.UnitID(unitID), unitaccount.BalanceGT(decimal.Zero)).Count(ctx); err != nil {
		return nil, err
	} else if n > 0 {
		return nil, httpx.Conflict("the unit still owes; clear every account (and issue the clearance certificate) before transferring")
	}
	current, err := s.client.UnitParty.Query().Where(unitparty.UnitID(unitID),
		unitparty.RoleIn(unitparty.RoleOwner, unitparty.RoleJointOwner, unitparty.RoleBuyer, unitparty.RoleLandlord), ActiveLink()).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, l := range current {
		if l.PartyID == np.ID {
			return nil, httpx.Conflict("this person already owns the unit")
		}
	}
	link, err := s.LinkParty(ctx, unitID, actor, LinkInput{PartyID: np.ID, Role: string(unitparty.RoleOwner), IsPrimary: true,
		StartDate: &date, Source: string(unitparty.SourceTransfer)})
	if err != nil {
		return nil, err
	}
	res := &TransferResult{NewLink: link}
	for _, l := range current {
		if _, err := s.EndLink(ctx, l.ID, date); err != nil {
			return nil, err
		}
		_ = s.client.UnitParty.UpdateOneID(l.ID).SetIsPrimary(false).Exec(ctx)
		res.EndedLinks++
	}
	// Accounts follow the new owner now when the date has passed (EndLink moved them); for a future
	// date the occupancy job moves them on the day.
	res.Apportion = apportion(date)
	if in.Reading != nil {
		use, err := s.handoverReading(ctx, u.ID, actor, res.Apportion.Period, decimal.NewFromFloat(*in.Reading), date)
		if err != nil {
			return nil, err
		}
		res.Apportion.HandoverUse = use
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	_ = events.Publish(ctx, s.client.OutboxEvent, tenantID, u.ID.String(), UnitTransferred, map[string]any{
		"unit_id": u.ID, "unit_code": u.Code, "property_id": u.PropertyID, "new_owner_party_id": np.ID,
		"date": date.Format("2006-01-02"), "ended_links": res.EndedLinks, "note": in.Note})
	if in.Invite && np.Phone != "" && s.auth != nil {
		if _, err := s.Invite(ctx, np.ID, tenantSlug, portalURL); err != nil {
			s.log.Warn("transfer: new owner invite", zap.Error(err))
		} else {
			res.Invited = true
		}
	}
	return res, nil
}

// apportion splits the date's month: days before the date are the outgoing owner's.
func apportion(date time.Time) Apportion {
	first := time.Date(date.Year(), date.Month(), 1, 0, 0, 0, 0, date.Location())
	days := first.AddDate(0, 1, -1).Day()
	out := date.Day() - 1
	return Apportion{Period: date.Format("2006-01"), DaysInPeriod: days, OutgoingDays: out, IncomingDays: days - out}
}

// handoverReading records the unit meter's reading at handover as a move-out reading (billing
// reads round, estimate, import and replacement readings, so it is never billed twice) and returns
// the water used this period up to it.
func (s *Service) handoverReading(ctx context.Context, unitID, by uuid.UUID, period string, reading decimal.Decimal, at time.Time) (*decimal.Decimal, error) {
	m, err := s.client.Meter.Query().Where(meter.UnitID(unitID), meter.StatusEQ(meter.StatusActive)).First(ctx)
	if ent.IsNotFound(err) {
		return nil, httpx.Invalid("this unit has no active meter to read")
	}
	if err != nil {
		return nil, err
	}
	prev := m.InitialReading
	if last, err := s.client.MeterReading.Query().Where(meterreading.MeterID(m.ID), meterreading.PeriodLT(period),
		meterreading.StatusNEQ(meterreading.StatusRejected)).Order(ent.Desc(meterreading.FieldPeriod), ent.Desc(meterreading.FieldReadAt)).
		First(ctx); err == nil {
		prev = last.Reading
	}
	if reading.LessThan(prev) {
		return nil, httpx.Invalid("the handover reading is below the last reading " + prev.StringFixed(0))
	}
	mult := m.Multiplier
	if !mult.IsPositive() {
		mult = decimal.NewFromInt(1)
	}
	use := reading.Sub(prev).Mul(mult)
	if _, err := s.client.MeterReading.Create().SetMeterID(m.ID).SetUnitID(unitID).SetPeriod(period).SetReading(reading).
		SetPreviousReading(prev).SetConsumption(use).SetSource(meterreading.SourceMoveOut).SetStatus(meterreading.StatusAccepted).
		SetReadAt(at).SetReadBy(by).SetNotes("Handover reading at transfer").Save(ctx); err != nil {
		return nil, err
	}
	return &use, nil
}
