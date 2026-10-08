// Package utilities runs meters, reading rounds with photo evidence and anomaly flags, estimates
// and the water balance (SRDD 10.2, 10.3).
package utilities

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/meter"
	"github.com/bengobox/maskani-api/internal/ent/meterreading"
	"github.com/bengobox/maskani-api/internal/ent/readinground"
	"github.com/bengobox/maskani-api/internal/ent/unit"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/platform/realtime"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
	"github.com/bengobox/maskani-api/internal/shared/sqlx"
)

// Flag codes on a reading.
const (
	FlagLower = "lower_than_previous"
	FlagZero  = "zero_occupied"
	FlagSpike = "spike"
)

// Service is the utilities domain service.
type Service struct {
	client *ent.Client
	log    *zap.Logger
	rt     realtime.Publisher
}

// SetRealtime sets the publisher for reading hints (nil disables them).
func (s *Service) SetRealtime(p realtime.Publisher) { s.rt = p }

// saved publishes reading.saved for a stored reading and passes the result through.
func (s *Service) saved(ctx context.Context, propertyID uuid.UUID, r *ent.MeterReading, err error) (*ent.MeterReading, error) {
	if err != nil || r == nil {
		return r, err
	}
	if propertyID == uuid.Nil {
		if m, merr := s.client.Meter.Get(ctx, r.MeterID); merr == nil {
			propertyID = m.PropertyID
		}
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	realtime.Emit(s.rt, tenantID, realtime.Event{Type: realtime.ReadingSaved, ID: r.ID.String(),
		PropertyID: propertyID.String(), UnitID: realtime.IDString(r.UnitID)})
	return r, nil
}

// NewService creates the utilities service.
func NewService(client *ent.Client, log *zap.Logger) *Service {
	return &Service{client: client, log: log.Named("utilities")}
}

// MeterInput creates a meter.
type MeterInput struct {
	PropertyID     uuid.UUID  `json:"property_id"`
	UnitID         *uuid.UUID `json:"unit_id"`
	Kind           string     `json:"kind"`
	Utility        string     `json:"utility"`
	Serial         string     `json:"serial"`
	Make           string     `json:"make"`
	LocationNote   string     `json:"location_note"`
	InitialReading float64    `json:"initial_reading"`
	Multiplier     float64    `json:"multiplier"`
	WalkingOrder   int        `json:"walking_order"`
	ChargeCode     string     `json:"charge_code"`
}

// CreateMeter registers a meter.
func (s *Service) CreateMeter(ctx context.Context, in MeterInput) (*ent.Meter, error) {
	if strings.TrimSpace(in.Serial) == "" {
		return nil, httpx.Invalid("serial is required")
	}
	kind := meter.Kind(in.Kind)
	if in.Kind == "" {
		kind = meter.KindUnit
	}
	if kind == meter.KindUnit && in.UnitID == nil {
		return nil, httpx.Invalid("a unit meter needs unit_id")
	}
	mult := in.Multiplier
	if mult <= 0 {
		mult = 1
	}
	c := s.client.Meter.Create().SetPropertyID(in.PropertyID).SetKind(kind).SetSerial(strings.TrimSpace(in.Serial)).
		SetMake(in.Make).SetLocationNote(in.LocationNote).SetMultiplier(decimal.NewFromFloat(mult)).
		SetInitialReading(decimal.NewFromFloat(in.InitialReading)).SetWalkingOrder(in.WalkingOrder).SetInstalledAt(time.Now())
	if in.UnitID != nil {
		c.SetUnitID(*in.UnitID)
	}
	if in.Utility != "" {
		c.SetUtility(meter.Utility(in.Utility))
	}
	if in.ChargeCode != "" {
		c.SetMetadata(map[string]any{"charge_code": in.ChargeCode})
	}
	return c.Save(ctx)
}

// ListMeters returns a property's meters.
func (s *Service) ListMeters(ctx context.Context, propertyID uuid.UUID) ([]*ent.Meter, error) {
	return s.client.Meter.Query().Where(meter.PropertyID(propertyID)).
		Order(ent.Asc(meter.FieldKind), ent.Asc(meter.FieldWalkingOrder), ent.Asc(meter.FieldSerial)).All(ctx)
}

// RoundRow is one meter on a round with its previous and current reading.
type RoundRow struct {
	MeterID         uuid.UUID         `json:"meter_id"`
	Serial          string            `json:"serial"`
	Kind            string            `json:"kind"`
	UnitID          *uuid.UUID        `json:"unit_id,omitempty"`
	UnitCode        string            `json:"unit_code,omitempty"`
	Block           string            `json:"block,omitempty"`
	PreviousReading decimal.Decimal   `json:"previous_reading"`
	Current         *ent.MeterReading `json:"current,omitempty"`
}

// Round is a property's reading round for a period.
type Round struct {
	*ent.ReadingRound
	Rows  []RoundRow `json:"rows"`
	Read  int        `json:"read"`
	Total int        `json:"total"`
}

// GetRound returns (and opens if needed) the round for a property and period, with meters in
// walking order, each with the last reading before the period and this period's reading.
func (s *Service) GetRound(ctx context.Context, propertyID uuid.UUID, period string) (*Round, error) {
	if _, err := time.Parse("2006-01", period); err != nil {
		return nil, httpx.Invalid("period must be YYYY-MM")
	}
	rr, err := s.client.ReadingRound.Query().Where(readinground.PropertyID(propertyID), readinground.Period(period)).Only(ctx)
	if ent.IsNotFound(err) {
		rr, err = s.client.ReadingRound.Create().SetPropertyID(propertyID).SetPeriod(period).SetOpenedAt(time.Now()).Save(ctx)
		if ent.IsConstraintError(err) {
			rr, err = s.client.ReadingRound.Query().Where(readinground.PropertyID(propertyID), readinground.Period(period)).Only(ctx)
		}
	}
	if err != nil {
		return nil, err
	}
	meters, err := s.client.Meter.Query().Where(meter.PropertyID(propertyID), meter.StatusEQ(meter.StatusActive)).All(ctx)
	if err != nil {
		return nil, err
	}
	unitIDs := []uuid.UUID{}
	meterIDs := make([]uuid.UUID, len(meters))
	for i, m := range meters {
		meterIDs[i] = m.ID
		if m.UnitID != nil {
			unitIDs = append(unitIDs, *m.UnitID)
		}
	}
	units, err := s.client.Unit.Query().Where(unit.IDIn(unitIDs...)).WithBlock().All(ctx)
	if err != nil {
		return nil, err
	}
	unitByID := map[uuid.UUID]*ent.Unit{}
	for _, u := range units {
		unitByID[u.ID] = u
	}
	current, err := s.client.MeterReading.Query().
		Where(meterreading.MeterIDIn(meterIDs...), meterreading.Period(period), meterreading.SourceIn(meterreading.SourceRound, meterreading.SourceEstimate)).All(ctx)
	if err != nil {
		return nil, err
	}
	curByMeter := map[uuid.UUID]*ent.MeterReading{}
	for _, r := range current {
		curByMeter[r.MeterID] = r
	}
	prevByMeter, err := s.previousReadings(ctx, meterIDs, period)
	if err != nil {
		return nil, err
	}
	out := &Round{ReadingRound: rr, Total: len(meters)}
	for _, m := range meters {
		row := RoundRow{MeterID: m.ID, Serial: m.Serial, Kind: string(m.Kind), UnitID: m.UnitID, PreviousReading: m.InitialReading}
		if p, ok := prevByMeter[m.ID]; ok {
			row.PreviousReading = p
		}
		if m.UnitID != nil {
			if u := unitByID[*m.UnitID]; u != nil {
				row.UnitCode = u.Code
				if u.Edges.Block != nil {
					row.Block = u.Edges.Block.Code
				}
			}
		}
		if c := curByMeter[m.ID]; c != nil {
			row.Current = c
			out.Read++
		}
		out.Rows = append(out.Rows, row)
	}
	order := map[uuid.UUID]int{}
	for _, m := range meters {
		order[m.ID] = m.WalkingOrder
	}
	sort.SliceStable(out.Rows, func(i, j int) bool {
		a, b := out.Rows[i], out.Rows[j]
		if a.Kind != b.Kind {
			return a.Kind == "unit"
		}
		if a.Block != b.Block {
			return a.Block < b.Block
		}
		if order[a.MeterID] != order[b.MeterID] {
			return order[a.MeterID] < order[b.MeterID]
		}
		return a.UnitCode < b.UnitCode
	})
	return out, nil
}

// previousReadings returns, per meter, the latest non-rejected reading from an earlier period
// within the last 24 months (bounded: at most meters x 24 rows). Older history falls back to the
// meter's initial reading.
func (s *Service) previousReadings(ctx context.Context, meterIDs []uuid.UUID, period string) (map[uuid.UUID]decimal.Decimal, error) {
	floor := period
	if t, err := time.Parse("2006-01", period); err == nil {
		floor = t.AddDate(-2, 0, 0).Format("2006-01")
	}
	rows, err := s.client.MeterReading.Query().
		Where(meterreading.MeterIDIn(meterIDs...), meterreading.PeriodLT(period), meterreading.PeriodGTE(floor),
			meterreading.StatusNEQ(meterreading.StatusRejected)).
		Order(ent.Desc(meterreading.FieldPeriod), ent.Desc(meterreading.FieldReadAt)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID]decimal.Decimal{}
	for _, r := range rows {
		if _, ok := out[r.MeterID]; !ok {
			out[r.MeterID] = r.Reading
		}
	}
	return out, nil
}

// ReadingInput records a reading. The meter photo is optional (user decision 2026-10-08): it helps
// settle disputes but a reading must not be blocked when the camera or upload fails.
type ReadingInput struct {
	Period   string     `json:"period"`
	Reading  float64    `json:"reading"`
	PhotoKey string     `json:"photo_key"`
	ReadAt   *time.Time `json:"read_at"`
	Notes    string     `json:"notes"`
}

// Record stores a reading (replacing an unaccepted one for the same period), computes consumption
// against the previous reading and flags anomalies for re-checking.
func (s *Service) Record(ctx context.Context, meterID, readBy uuid.UUID, in ReadingInput) (*ent.MeterReading, error) {
	m, err := s.client.Meter.Get(ctx, meterID)
	if err != nil {
		return nil, err
	}
	prevMap, err := s.previousReadings(ctx, []uuid.UUID{meterID}, in.Period)
	if err != nil {
		return nil, err
	}
	prev, ok := prevMap[meterID]
	if !ok {
		prev = m.InitialReading
	}
	reading := decimal.NewFromFloat(in.Reading)
	mult := m.Multiplier
	if !mult.IsPositive() {
		mult = decimal.NewFromInt(1)
	}
	consumption := reading.Sub(prev).Mul(mult)
	flags := []string{}
	if reading.LessThan(prev) {
		flags = append(flags, FlagLower)
		consumption = decimal.Zero
	}
	if consumption.IsZero() && m.UnitID != nil {
		if u, err := s.client.Unit.Get(ctx, *m.UnitID); err == nil && u.OccupancyStatus != unit.OccupancyStatusVacant {
			flags = append(flags, FlagZero)
		}
	}
	if avg := s.average(ctx, meterID, in.Period); avg.IsPositive() && consumption.GreaterThan(avg.Mul(decimal.NewFromInt(3))) {
		flags = append(flags, FlagSpike)
	}
	status := meterreading.StatusAccepted
	if len(flags) > 0 {
		status = meterreading.StatusPending
	}
	at := time.Now()
	if in.ReadAt != nil {
		at = *in.ReadAt
	}
	rr, _ := s.client.ReadingRound.Query().Where(readinground.PropertyID(m.PropertyID), readinground.Period(in.Period)).Only(ctx)
	existing, err := s.client.MeterReading.Query().
		Where(meterreading.MeterID(meterID), meterreading.Period(in.Period), meterreading.SourceEQ(meterreading.SourceRound)).Only(ctx)
	if err == nil {
		if existing.Status == meterreading.StatusAccepted && len(flags) == 0 && existing.Reading.Equal(reading) {
			return existing, nil
		}
		r, err := existing.Update().SetReading(reading).SetPreviousReading(prev).SetConsumption(consumption).
			SetFlags(flags).SetStatus(status).SetPhotoKey(in.PhotoKey).SetReadAt(at).SetReadBy(readBy).SetNotes(in.Notes).Save(ctx)
		return s.saved(ctx, m.PropertyID, r, err)
	}
	c := s.client.MeterReading.Create().SetMeterID(meterID).SetPeriod(in.Period).SetReading(reading).
		SetPreviousReading(prev).SetConsumption(consumption).SetFlags(flags).SetStatus(status).
		SetPhotoKey(in.PhotoKey).SetReadAt(at).SetReadBy(readBy).SetNotes(in.Notes)
	if m.UnitID != nil {
		c.SetUnitID(*m.UnitID)
	}
	if rr != nil {
		c.SetRoundID(rr.ID)
	}
	r, err := c.Save(ctx)
	return s.saved(ctx, m.PropertyID, r, err)
}

// average is the mean consumption over the meter's last three periods.
func (s *Service) average(ctx context.Context, meterID uuid.UUID, period string) decimal.Decimal {
	rows, err := s.client.MeterReading.Query().
		Where(meterreading.MeterID(meterID), meterreading.PeriodLT(period), meterreading.StatusNEQ(meterreading.StatusRejected)).
		Order(ent.Desc(meterreading.FieldPeriod)).Limit(3).All(ctx)
	if err != nil || len(rows) == 0 {
		return decimal.Zero
	}
	sum := decimal.Zero
	for _, r := range rows {
		sum = sum.Add(r.Consumption)
	}
	return sum.Div(decimal.NewFromInt(int64(len(rows))))
}

// Verify accepts, rejects or asks for a re-check of a flagged reading.
func (s *Service) Verify(ctx context.Context, readingID, by uuid.UUID, action string) (*ent.MeterReading, error) {
	st := map[string]meterreading.Status{"accept": meterreading.StatusAccepted, "reject": meterreading.StatusRejected, "recheck": meterreading.StatusRecheck}[action]
	if st == "" {
		return nil, httpx.Invalid("action must be accept, reject or recheck")
	}
	r, err := s.client.MeterReading.UpdateOneID(readingID).SetStatus(st).SetVerifiedBy(by).Save(ctx)
	return s.saved(ctx, uuid.Nil, r, err)
}

// Estimate records an estimated reading at the three-period average when a meter cannot be read.
// The next actual reading corrects it automatically because consumption is always measured from
// the last stored reading.
func (s *Service) Estimate(ctx context.Context, meterID, by uuid.UUID, period string) (*ent.MeterReading, error) {
	m, err := s.client.Meter.Get(ctx, meterID)
	if err != nil {
		return nil, err
	}
	prevMap, err := s.previousReadings(ctx, []uuid.UUID{meterID}, period)
	if err != nil {
		return nil, err
	}
	prev, ok := prevMap[meterID]
	if !ok {
		prev = m.InitialReading
	}
	avg := s.average(ctx, meterID, period).Round(0)
	c := s.client.MeterReading.Create().SetMeterID(meterID).SetPeriod(period).SetReading(prev.Add(avg)).
		SetPreviousReading(prev).SetConsumption(avg).SetSource(meterreading.SourceEstimate).SetIsEstimated(true).
		SetStatus(meterreading.StatusAccepted).SetReadAt(time.Now()).SetReadBy(by).SetNotes("estimated at the 3 month average")
	if m.UnitID != nil {
		c.SetUnitID(*m.UnitID)
	}
	r, err := c.Save(ctx)
	return s.saved(ctx, m.PropertyID, r, err)
}

// BalancePoint is one period of the water balance.
type BalancePoint struct {
	Period    string          `json:"period"`
	Supplied  decimal.Decimal `json:"supplied_m3"`
	Billed    decimal.Decimal `json:"billed_m3"`
	Common    decimal.Decimal `json:"common_m3"`
	Lost      decimal.Decimal `json:"unaccounted_m3"`
	LossPct   decimal.Decimal `json:"loss_pct"`
	Estimated int             `json:"estimated_readings"`
}

// WaterBalance returns supplied vs billed vs common water for the period and the five before it,
// aggregated in SQL per meter and period.
func (s *Service) WaterBalance(ctx context.Context, propertyID uuid.UUID, period string) ([]BalancePoint, error) {
	end, err := time.Parse("2006-01", period)
	if err != nil {
		return nil, httpx.Invalid("period must be YYYY-MM")
	}
	from := end.AddDate(0, -5, 0).Format("2006-01")
	meters, err := s.client.Meter.Query().Where(meter.PropertyID(propertyID)).All(ctx)
	if err != nil {
		return nil, err
	}
	kind := map[uuid.UUID]meter.Kind{}
	ids := make([]uuid.UUID, len(meters))
	for i, m := range meters {
		kind[m.ID], ids[i] = m.Kind, m.ID
	}
	var rows []struct {
		MeterID   uuid.UUID       `json:"meter_id"`
		Period    string          `json:"period"`
		Used      decimal.Decimal `json:"used"`
		Estimated int             `json:"estimated"`
	}
	if len(ids) > 0 {
		err = s.client.MeterReading.Query().
			Where(meterreading.MeterIDIn(ids...), meterreading.PeriodGTE(from), meterreading.PeriodLTE(period),
				meterreading.StatusNEQ(meterreading.StatusRejected)).
			GroupBy(meterreading.FieldMeterID, meterreading.FieldPeriod).
			Aggregate(sqlx.SumAs(meterreading.FieldConsumption, "used", ""), sqlx.CountAs("estimated", "is_estimated")).
			Scan(ctx, &rows)
		if err != nil {
			return nil, err
		}
	}
	byPeriod := map[string]*BalancePoint{}
	for t := end.AddDate(0, -5, 0); !t.After(end); t = t.AddDate(0, 1, 0) {
		p := t.Format("2006-01")
		byPeriod[p] = &BalancePoint{Period: p}
	}
	for _, r := range rows {
		bp := byPeriod[r.Period]
		if bp == nil {
			continue
		}
		switch kind[r.MeterID] {
		case meter.KindBulkSupply, meter.KindBorehole:
			bp.Supplied = bp.Supplied.Add(r.Used)
		case meter.KindCommonArea:
			bp.Common = bp.Common.Add(r.Used)
		default:
			bp.Billed = bp.Billed.Add(r.Used)
			bp.Estimated += r.Estimated
		}
	}
	out := make([]BalancePoint, 0, len(byPeriod))
	for _, bp := range byPeriod {
		bp.Lost = bp.Supplied.Sub(bp.Billed).Sub(bp.Common)
		if bp.Supplied.IsPositive() {
			bp.LossPct = bp.Lost.Div(bp.Supplied).Mul(decimal.NewFromInt(100)).Round(1)
		}
		out = append(out, *bp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Period < out[j].Period })
	return out, nil
}
