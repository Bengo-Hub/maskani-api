package utilities

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/meter"
	"github.com/bengobox/maskani-api/internal/ent/meterreading"
	"github.com/bengobox/maskani-api/internal/http/httpx"
)

// ReplaceInput swaps a meter: the old meter's closing reading, and the new meter's serial and
// opening reading. The period is the billing period the closing reading belongs to.
type ReplaceInput struct {
	Period         string     `json:"period"`
	ClosingReading float64    `json:"closing_reading"`
	NewSerial      string     `json:"new_serial"`
	NewMake        string     `json:"new_make"`
	OpeningReading float64    `json:"opening_reading"`
	ReadAt         *time.Time `json:"read_at"`
	PhotoKey       string     `json:"photo_key"`
	Notes          string     `json:"notes"`
}

// ReplaceMeter closes a meter with its final reading (source replacement, billed with the period's
// round) and puts a new meter in its place from its opening reading, in one transaction. The new
// meter takes the old one's unit, kind, utility, multiplier, walking order, location and charge, so
// the round and the bill carry on; the period's use is the old meter's last stretch plus the new
// meter's use from its opening reading.
func (s *Service) ReplaceMeter(ctx context.Context, meterID, by uuid.UUID, in ReplaceInput) (*ent.Meter, error) {
	in.NewSerial = strings.TrimSpace(in.NewSerial)
	if _, err := time.Parse("2006-01", in.Period); err != nil {
		return nil, httpx.Invalid("period must be YYYY-MM")
	}
	if in.NewSerial == "" {
		return nil, httpx.Invalid("the new meter's serial is required")
	}
	if in.OpeningReading < 0 || in.ClosingReading < 0 {
		return nil, httpx.Invalid("readings cannot be negative")
	}
	old, err := s.client.Meter.Get(ctx, meterID)
	if err != nil {
		return nil, err
	}
	if old.Status != meter.StatusActive {
		return nil, httpx.Conflict("only an active meter can be replaced")
	}
	prevMap, err := s.previousReadings(ctx, []uuid.UUID{meterID}, in.Period)
	if err != nil {
		return nil, err
	}
	prev, ok := prevMap[meterID]
	if !ok {
		prev = old.InitialReading
	}
	closing := decimal.NewFromFloat(in.ClosingReading)
	if closing.LessThan(prev) {
		return nil, httpx.Invalid("the closing reading is below the last reading " + prev.StringFixed(0))
	}
	mult := old.Multiplier
	if !mult.IsPositive() {
		mult = decimal.NewFromInt(1)
	}
	at := time.Now()
	if in.ReadAt != nil {
		at = *in.ReadAt
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	c := tx.MeterReading.Create().SetMeterID(old.ID).SetPeriod(in.Period).SetReading(closing).SetPreviousReading(prev).
		SetConsumption(closing.Sub(prev).Mul(mult)).SetSource(meterreading.SourceReplacement).SetStatus(meterreading.StatusAccepted).
		SetReadAt(at).SetReadBy(by).SetPhotoKey(in.PhotoKey).SetNotes(strings.TrimSpace("Closing reading at replacement. " + in.Notes))
	if old.UnitID != nil {
		c.SetUnitID(*old.UnitID)
	}
	if _, err := c.Save(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	nc := tx.Meter.Create().SetPropertyID(old.PropertyID).SetNillableUnitID(old.UnitID).SetKind(old.Kind).SetUtility(old.Utility).
		SetSerial(in.NewSerial).SetMake(in.NewMake).SetLocationNote(old.LocationNote).SetMultiplier(mult).
		SetInitialReading(decimal.NewFromFloat(in.OpeningReading)).SetWalkingOrder(old.WalkingOrder).SetInstalledAt(at).
		SetMetadata(old.Metadata)
	nm, err := nc.Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	// Conditional on active, so two replacements at once cannot both close the meter.
	n, err := tx.Meter.Update().Where(meter.ID(old.ID), meter.StatusEQ(meter.StatusActive)).
		SetStatus(meter.StatusReplaced).SetClosingReading(closing).SetReplacedByID(nm.ID).Save(ctx)
	if err != nil || n == 0 {
		_ = tx.Rollback()
		if err == nil {
			err = httpx.Conflict("this meter was replaced at the same moment")
		}
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return nm, nil
}

// BatchReading is one line of a batch.
type BatchReading struct {
	MeterID  uuid.UUID  `json:"meter_id"`
	Reading  float64    `json:"reading"`
	ReadAt   *time.Time `json:"read_at"`
	Notes    string     `json:"notes"`
	PhotoKey string     `json:"photo_key"`
}

// BatchResult says what happened to one line.
type BatchResult struct {
	MeterID uuid.UUID         `json:"meter_id"`
	Reading *ent.MeterReading `json:"reading,omitempty"`
	Error   string            `json:"error,omitempty"`
}

// RecordBatch records many readings of one property and period at once (bulk supply and borehole
// meters from a sheet, or a round captured offline). Each line goes through Record, so flags and
// replacements behave the same; meters of other properties are refused line by line.
func (s *Service) RecordBatch(ctx context.Context, propertyID, by uuid.UUID, period string, lines []BatchReading) ([]BatchResult, error) {
	if _, err := time.Parse("2006-01", period); err != nil {
		return nil, httpx.Invalid("period must be YYYY-MM")
	}
	if len(lines) == 0 || len(lines) > 500 {
		return nil, httpx.Invalid("send between 1 and 500 readings")
	}
	ids := make([]uuid.UUID, len(lines))
	for i, l := range lines {
		ids[i] = l.MeterID
	}
	ok := map[uuid.UUID]bool{}
	ms, err := s.client.Meter.Query().Where(meter.IDIn(ids...), meter.PropertyID(propertyID), meter.StatusEQ(meter.StatusActive)).
		Select(meter.FieldID).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range ms {
		ok[m.ID] = true
	}
	out := make([]BatchResult, len(lines))
	for i, l := range lines {
		out[i].MeterID = l.MeterID
		if !ok[l.MeterID] {
			out[i].Error = "not an active meter of this property"
			continue
		}
		r, err := s.Record(ctx, l.MeterID, by, ReadingInput{Period: period, Reading: l.Reading, ReadAt: l.ReadAt, Notes: l.Notes, PhotoKey: l.PhotoKey})
		if err != nil {
			out[i].Error = err.Error()
			continue
		}
		out[i].Reading = r
	}
	return out, nil
}
