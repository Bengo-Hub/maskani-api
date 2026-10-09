package billing

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/billingrun"
	"github.com/bengobox/maskani-api/internal/ent/chargetype"
	"github.com/bengobox/maskani-api/internal/ent/fund"
	"github.com/bengobox/maskani-api/internal/ent/maskaniuseroutlet"
	"github.com/bengobox/maskani-api/internal/ent/meter"
	"github.com/bengobox/maskani-api/internal/ent/meterreading"
	"github.com/bengobox/maskani-api/internal/ent/property"
	"github.com/bengobox/maskani-api/internal/ent/unit"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/register"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// Billing schedules. A property can bill itself on the estate's billing day. Charges that need
// no input (service charge, garbage, levies) are always ready; metered charges (water) need the
// month's readings. Before the billing day the schedule reminds the reading takers about meters
// still unread. On the day it runs the bill when every reading is in. If some are missing it
// either runs anyway (policy "skip": the unread units get no water line this month) or waits
// (policy "wait", the default), reminding daily until the readings arrive or someone with
// billing.run approves running without them. Mode "remind" never runs on its own: it tells
// finance the run is ready instead.
//
// The schedule lives in the property's metadata (no new table); its state is the per-period
// approval and the day each alert last went out, trimmed to the last few periods. Runs go through
// Issue, which returns the existing run for a period, so a run is never made twice.

const scheduleKey = "billing_schedule"

// Schedule is a property's billing schedule.
type Schedule struct {
	Enabled          bool   `json:"enabled"`
	Fund             string `json:"fund"`
	Mode             string `json:"mode"`             // auto or remind
	MissingReadings  string `json:"missing_readings"` // wait or skip
	RemindDaysBefore int    `json:"remind_days_before"`
	// Approved: period to the user who let it run without the missing readings.
	Approved map[string]string `json:"approved,omitempty"`
	// Sent: alert key ("2026-10:reminder") to the last day it went out.
	Sent map[string]string `json:"sent,omitempty"`
}

func (sc *Schedule) defaults() {
	if sc.Fund == "" {
		sc.Fund = "estate"
	}
	if sc.Mode != "remind" {
		sc.Mode = "auto"
	}
	if sc.MissingReadings != "skip" {
		sc.MissingReadings = "wait"
	}
	if sc.RemindDaysBefore <= 0 {
		sc.RemindDaysBefore = 3
	}
	if sc.RemindDaysBefore > 20 {
		sc.RemindDaysBefore = 20
	}
}

func readSchedule(p *ent.Property) Schedule {
	var sc Schedule
	if raw, ok := p.Metadata[scheduleKey]; ok {
		b, _ := json.Marshal(raw)
		_ = json.Unmarshal(b, &sc)
	}
	sc.defaults()
	return sc
}

// trim keeps the state of the last three periods only.
func (sc *Schedule) trim(period string) {
	keep := map[string]bool{}
	if t, err := time.Parse("2006-01", period); err == nil {
		for i := -2; i <= 1; i++ {
			keep[t.AddDate(0, i, 0).Format("2006-01")] = true
		}
	}
	for k := range sc.Approved {
		if !keep[k] {
			delete(sc.Approved, k)
		}
	}
	for k := range sc.Sent {
		if !keep[strings.SplitN(k, ":", 2)[0]] {
			delete(sc.Sent, k)
		}
	}
}

func (s *Service) writeSchedule(ctx context.Context, p *ent.Property, sc Schedule) error {
	b, _ := json.Marshal(sc)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	meta := make(map[string]any, len(p.Metadata)+1)
	for k, v := range p.Metadata {
		meta[k] = v
	}
	meta[scheduleKey] = m
	return s.client.Property.UpdateOneID(p.ID).SetMetadata(meta).Exec(ctx)
}

// ScheduleInput changes a schedule; absent fields stay as they are.
type ScheduleInput struct {
	Enabled          *bool   `json:"enabled"`
	Fund             *string `json:"fund"`
	Mode             *string `json:"mode"`
	MissingReadings  *string `json:"missing_readings"`
	RemindDaysBefore *int    `json:"remind_days_before"`
}

// MissingReading is a meter with no reading for the period.
type MissingReading struct {
	MeterID  uuid.UUID  `json:"meter_id"`
	Serial   string     `json:"serial"`
	UnitID   *uuid.UUID `json:"unit_id,omitempty"`
	UnitCode string     `json:"unit_code,omitempty"`
}

// ScheduleView is a schedule with where this property's next scheduled run stands.
type ScheduleView struct {
	Schedule
	BillingDay  int       `json:"billing_day"`
	Period      string    `json:"period"`
	BillingDate time.Time `json:"billing_date"`
	// Stage: off, scheduled, collecting_readings, waiting_for_readings (policy wait, day reached),
	// ready_to_run (mode remind), due (auto: runs within the hour) or issued.
	Stage        string           `json:"stage"`
	Metered      bool             `json:"metered"`
	MissingCount int              `json:"missing_count"`
	Missing      []MissingReading `json:"missing"`
	RunID        *uuid.UUID       `json:"run_id,omitempty"`
	RunStatus    string           `json:"run_status,omitempty"`
	ApprovedBy   string           `json:"approved_by,omitempty"`
}

type schedTarget struct {
	period string
	date   time.Time
}

// targets are this month's and next month's billing dates with the period each bills. Readings
// for a month are taken in its last days (the reading window), so a billing day after the window
// bills the same month and an earlier day (the 1st, say) bills the month before.
func targets(now time.Time, loc *time.Location, billingDay, windowEnd int) []schedTarget {
	m := time.Date(now.In(loc).Year(), now.In(loc).Month(), 1, 0, 0, 0, 0, loc)
	out := make([]schedTarget, 0, 2)
	for _, start := range []time.Time{m, m.AddDate(0, 1, 0)} {
		per := start
		if billingDay <= windowEnd {
			per = start.AddDate(0, -1, 0)
		}
		out = append(out, schedTarget{period: per.Format("2006-01"), date: start.AddDate(0, 0, billingDay-1)})
	}
	return out
}

// missingReadings lists the property's active unit meters with no reading for the period
// (rejected readings do not count), when the fund bills a metered charge at all.
func (s *Service) missingReadings(ctx context.Context, propertyID uuid.UUID, fundCode, period string, limit int) ([]MissingReading, int, bool, error) {
	metered, err := s.client.ChargeType.Query().Where(chargetype.Active(true), chargetype.FundCode(fundCode),
		chargetype.BasisEQ(chargetype.BasisMetered)).Exist(ctx)
	if err != nil || !metered {
		return nil, 0, false, err
	}
	meters, err := s.client.Meter.Query().Where(meter.PropertyID(propertyID), meter.KindEQ(meter.KindUnit),
		meter.UnitIDNotNil(), meter.StatusEQ(meter.StatusActive)).
		Select(meter.FieldID, meter.FieldSerial, meter.FieldUnitID).All(ctx)
	if err != nil || len(meters) == 0 {
		return nil, 0, true, err
	}
	ids := make([]uuid.UUID, len(meters))
	for i, m := range meters {
		ids[i] = m.ID
	}
	var read []struct {
		MeterID uuid.UUID `json:"meter_id"`
	}
	if err := s.client.MeterReading.Query().Where(meterreading.MeterIDIn(ids...), meterreading.Period(period),
		meterreading.StatusNEQ(meterreading.StatusRejected)).
		Select(meterreading.FieldMeterID).Scan(ctx, &read); err != nil {
		return nil, 0, true, err
	}
	done := make(map[uuid.UUID]bool, len(read))
	for _, r := range read {
		done[r.MeterID] = true
	}
	var missing []MissingReading
	var unitIDs []uuid.UUID
	for _, m := range meters {
		if !done[m.ID] {
			missing = append(missing, MissingReading{MeterID: m.ID, Serial: m.Serial, UnitID: m.UnitID})
			unitIDs = append(unitIDs, *m.UnitID)
		}
	}
	if len(missing) == 0 {
		return nil, 0, true, nil
	}
	codes := map[uuid.UUID]string{}
	if us, err := s.client.Unit.Query().Where(unit.IDIn(unitIDs...)).Select(unit.FieldID, unit.FieldCode).All(ctx); err == nil {
		for _, u := range us {
			codes[u.ID] = u.Code
		}
	}
	for i := range missing {
		missing[i].UnitCode = codes[*missing[i].UnitID]
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i].UnitCode < missing[j].UnitCode })
	total := len(missing)
	if len(missing) > limit {
		missing = missing[:limit]
	}
	return missing, total, true, nil
}

// tenantDays reads the estate's billing day and the last day of its reading window.
func (s *Service) tenantDays(ctx context.Context) (int, int) {
	billingDay, windowEnd := 1, 28
	if st, err := s.client.TenantSetting.Query().First(ctx); err == nil {
		billingDay, windowEnd = st.BillingDay, st.ReadingWindowEnd
	}
	if billingDay < 1 || billingDay > 28 {
		billingDay = 1
	}
	return billingDay, windowEnd
}

// view works out the stage of a property's next scheduled run.
func (s *Service) view(ctx context.Context, p *ent.Property, sc Schedule, now time.Time, billingDay, windowEnd int) (*ScheduleView, error) {
	v := &ScheduleView{Schedule: sc, BillingDay: billingDay, Missing: []MissingReading{}}
	f, err := s.client.Fund.Query().Where(fund.Code(sc.Fund)).Only(ctx)
	if err != nil {
		return nil, httpx.Invalid("fund not configured: " + sc.Fund)
	}
	ts := targets(now, s.loc, billingDay, windowEnd)
	t := ts[0]
	run, err := s.client.BillingRun.Query().Where(billingrun.PropertyID(p.ID), billingrun.FundID(f.ID),
		billingrun.Period(t.period), billingrun.RunKindEQ(billingrun.RunKindRegular),
		billingrun.StatusNEQ(billingrun.StatusCancelled)).Only(ctx)
	if err == nil {
		// This month's run is done: the next one is the target.
		t = ts[1]
		run, err = s.client.BillingRun.Query().Where(billingrun.PropertyID(p.ID), billingrun.FundID(f.ID),
			billingrun.Period(t.period), billingrun.RunKindEQ(billingrun.RunKindRegular),
			billingrun.StatusNEQ(billingrun.StatusCancelled)).Only(ctx)
	}
	v.Period, v.BillingDate, v.ApprovedBy = t.period, t.date, sc.Approved[t.period]
	if err == nil {
		v.RunID, v.RunStatus, v.Stage = &run.ID, string(run.Status), "issued"
		return v, nil
	}
	if !ent.IsNotFound(err) {
		return nil, err
	}
	missing, count, metered, err := s.missingReadings(ctx, p.ID, sc.Fund, t.period, 200)
	if err != nil {
		return nil, err
	}
	if missing != nil {
		v.Missing = missing
	}
	v.MissingCount, v.Metered = count, metered
	today := time.Date(now.In(s.loc).Year(), now.In(s.loc).Month(), now.In(s.loc).Day(), 0, 0, 0, 0, s.loc)
	switch {
	case !sc.Enabled:
		v.Stage = "off"
	case today.Before(t.date.AddDate(0, 0, -sc.RemindDaysBefore)):
		v.Stage = "scheduled"
	case today.Before(t.date):
		v.Stage = "scheduled"
		if count > 0 {
			v.Stage = "collecting_readings"
		}
	case count > 0 && sc.MissingReadings == "wait" && v.ApprovedBy == "":
		v.Stage = "waiting_for_readings"
	case sc.Mode == "remind":
		v.Stage = "ready_to_run"
	default:
		v.Stage = "due"
	}
	return v, nil
}

// ScheduleStatus is GET /billing-schedule: the property's schedule and its next run.
func (s *Service) ScheduleStatus(ctx context.Context, propertyID uuid.UUID) (*ScheduleView, error) {
	p, err := s.client.Property.Get(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	billingDay, windowEnd := s.tenantDays(ctx)
	return s.view(ctx, p, readSchedule(p), time.Now(), billingDay, windowEnd)
}

// SaveSchedule changes a property's schedule.
func (s *Service) SaveSchedule(ctx context.Context, propertyID uuid.UUID, in ScheduleInput) (*ScheduleView, error) {
	p, err := s.client.Property.Get(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	sc := readSchedule(p)
	if in.Enabled != nil {
		sc.Enabled = *in.Enabled
	}
	if in.Fund != nil {
		if ok, _ := s.client.Fund.Query().Where(fund.Code(*in.Fund)).Exist(ctx); !ok {
			return nil, httpx.Invalid("fund not configured: " + *in.Fund)
		}
		sc.Fund = *in.Fund
	}
	if in.Mode != nil {
		if *in.Mode != "auto" && *in.Mode != "remind" {
			return nil, httpx.Invalid("mode must be auto or remind")
		}
		sc.Mode = *in.Mode
	}
	if in.MissingReadings != nil {
		if *in.MissingReadings != "wait" && *in.MissingReadings != "skip" {
			return nil, httpx.Invalid("missing_readings must be wait or skip")
		}
		sc.MissingReadings = *in.MissingReadings
	}
	if in.RemindDaysBefore != nil {
		if *in.RemindDaysBefore < 1 || *in.RemindDaysBefore > 20 {
			return nil, httpx.Invalid("remind_days_before must be between 1 and 20")
		}
		sc.RemindDaysBefore = *in.RemindDaysBefore
	}
	sc.defaults()
	if err := s.writeSchedule(ctx, p, sc); err != nil {
		return nil, err
	}
	return s.ScheduleStatus(ctx, propertyID)
}

// ApproveScheduledRun runs the period now without the missing readings: the unread units get no
// metered line this month. The approval is kept on the schedule for the record.
func (s *Service) ApproveScheduledRun(ctx context.Context, actor, propertyID uuid.UUID, period string) (*ent.BillingRun, error) {
	p, err := s.client.Property.Get(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	if _, err := time.Parse("2006-01", period); err != nil {
		return nil, httpx.Invalid("period must be YYYY-MM")
	}
	sc := readSchedule(p)
	if sc.Approved == nil {
		sc.Approved = map[string]string{}
	}
	sc.Approved[period] = actor.String()
	sc.trim(period)
	if err := s.writeSchedule(ctx, p, sc); err != nil {
		return nil, err
	}
	return s.Issue(ctx, actor, IssueInput{PropertyID: propertyID, Fund: sc.Fund, Period: period})
}

// RunSchedules is the hourly job: for each tenant (billing on), each property with a schedule on
// moves one step: an alert at most once a day per stage, or the run when it is due.
func (s *Service) RunSchedules(ctx context.Context, tenants []uuid.UUID) (int, error) {
	runs := 0
	now := time.Now()
	for _, tid := range tenants {
		tctx := tenantguard.With(ctx, tid)
		props, err := s.client.Property.Query().Where(property.StatusEQ(property.StatusActive),
			func(sel *entsql.Selector) {
				sel.Where(sqljson.HasKey(property.FieldMetadata, sqljson.Path(scheduleKey)))
			}).
			All(tctx)
		if err != nil {
			s.log.Warn("billing schedules: properties", zap.String("tenant", tid.String()), zap.Error(err))
			continue
		}
		if len(props) == 0 {
			continue
		}
		billingDay, windowEnd := s.tenantDays(tctx)
		for _, p := range props {
			sc := readSchedule(p)
			if !sc.Enabled {
				continue
			}
			ran, err := s.tick(tctx, tid, p, sc, now, billingDay, windowEnd)
			if err != nil {
				s.log.Warn("billing schedule failed", zap.String("property", p.ID.String()), zap.Error(err))
			}
			if ran {
				runs++
			}
		}
	}
	return runs, nil
}

func (s *Service) tick(ctx context.Context, tenantID uuid.UUID, p *ent.Property, sc Schedule, now time.Time, billingDay, windowEnd int) (bool, error) {
	v, err := s.view(ctx, p, sc, now, billingDay, windowEnd)
	if err != nil {
		return false, err
	}
	if v.Stage == "due" {
		_, err := s.Issue(ctx, uuid.Nil, IssueInput{PropertyID: p.ID, Fund: sc.Fund, Period: v.Period})
		return err == nil, err
	}
	var evt, key string
	var roles []maskaniuseroutlet.PropertyRole
	day := now.In(s.loc).Format("2006-01-02")
	switch v.Stage {
	case "collecting_readings":
		evt, key = events.BillingReadingsMissing, v.Period+":reminder"
		roles = []maskaniuseroutlet.PropertyRole{maskaniuseroutlet.PropertyRoleCaretaker, maskaniuseroutlet.PropertyRolePropertyManager}
	case "waiting_for_readings":
		evt, key = events.BillingReadingsMissing, v.Period+":waiting"
		roles = []maskaniuseroutlet.PropertyRole{maskaniuseroutlet.PropertyRoleCaretaker, maskaniuseroutlet.PropertyRolePropertyManager,
			maskaniuseroutlet.PropertyRoleFinance}
	case "ready_to_run":
		evt, key = events.BillingRunReady, v.Period+":ready"
		roles = []maskaniuseroutlet.PropertyRole{maskaniuseroutlet.PropertyRoleFinance, maskaniuseroutlet.PropertyRolePropertyManager}
		if sc.Sent[key] != "" {
			return false, nil // a ready run is announced once
		}
	default:
		return false, nil
	}
	if sc.Sent[key] == day {
		return false, nil
	}
	units := make([]string, 0, 10)
	for _, m := range v.Missing {
		if len(units) == 10 {
			break
		}
		units = append(units, m.UnitCode)
	}
	payload := map[string]any{
		"property_id": p.ID, "property": p.Name, "period": v.Period, "fund": sc.Fund,
		"stage": strings.SplitN(key, ":", 2)[1], "billing_date": v.BillingDate.Format("2 Jan"),
		"missing_count": v.MissingCount, "missing_units": strings.Join(units, ", "), "mode": sc.Mode,
		"missing_readings": sc.MissingReadings,
	}
	if rs, _, err := register.PropertyResponders(ctx, s.client, p.ID, roles, 10); err == nil && len(rs) > 0 {
		payload["responders"] = rs
	}
	if err := events.Publish(ctx, s.client.OutboxEvent, tenantID, p.ID.String(), evt, payload); err != nil {
		return false, err
	}
	if sc.Sent == nil {
		sc.Sent = map[string]string{}
	}
	sc.Sent[key] = day
	sc.trim(v.Period)
	return false, s.writeSchedule(ctx, p, sc)
}

// startedBy is nil for a run the schedule starts on its own.
func startedBy(actor uuid.UUID) *uuid.UUID {
	if actor == uuid.Nil {
		return nil
	}
	return &actor
}
