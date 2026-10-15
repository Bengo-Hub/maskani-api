package works

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/serviceschedule"
	"github.com/bengobox/maskani-api/internal/ent/servicevisit"
	"github.com/bengobox/maskani-api/internal/ent/vendorcontract"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// visitHorizon is how far ahead visits are planned; the daily job keeps a week in view.
const visitHorizon = 7

// ScheduleInput is a recurring visit plan under a contract (garbage days, cleaning rounds).
type ScheduleInput struct {
	Name       string           `json:"name"`
	Frequency  string           `json:"frequency"` // daily, weekly, monthly, quarterly, half_yearly
	Days       []int            `json:"days"`      // weekdays 0 (Sunday) to 6 for weekly; month days 1 to 28 otherwise
	TimeWindow string           `json:"time_window"`
	Zone       string           `json:"zone"`
	Checklist  []map[string]any `json:"checklist"` // [{"item": "Bins emptied"}]
	Active     *bool            `json:"active"`
}

func (in *ScheduleInput) check() error {
	in.Name = strings.TrimSpace(in.Name)
	f := serviceschedule.Frequency(in.Frequency)
	if in.Name == "" {
		return httpx.Invalid("name the visit plan")
	}
	if serviceschedule.FrequencyValidator(f) != nil {
		return httpx.Invalid("frequency must be daily, weekly, monthly, quarterly or half_yearly")
	}
	for _, d := range in.Days {
		if (f == serviceschedule.FrequencyWeekly && (d < 0 || d > 6)) || (f != serviceschedule.FrequencyWeekly && (d < 1 || d > 28)) {
			return httpx.Invalid("weekly days are 0 (Sunday) to 6; other plans use month days 1 to 28")
		}
	}
	if len(in.Days) == 0 && f != serviceschedule.FrequencyDaily {
		in.Days = []int{1}
	}
	if len(in.Checklist) > 50 {
		return httpx.Invalid("at most 50 checklist items")
	}
	return nil
}

// CreateSchedule adds a visit plan to an active contract.
func (s *Service) CreateSchedule(ctx context.Context, contractID uuid.UUID, in ScheduleInput) (*ent.ServiceSchedule, error) {
	if err := in.check(); err != nil {
		return nil, err
	}
	c, err := s.client.VendorContract.Get(ctx, contractID)
	if err != nil {
		return nil, err
	}
	if c.Status != vendorcontract.StatusActive {
		return nil, httpx.Conflict("visits are planned on an active contract")
	}
	return s.client.ServiceSchedule.Create().SetContractID(c.ID).SetPropertyID(c.PropertyID).SetName(in.Name).
		SetFrequency(serviceschedule.Frequency(in.Frequency)).SetDays(in.Days).SetTimeWindow(in.TimeWindow).
		SetZone(in.Zone).SetChecklist(in.Checklist).Save(ctx)
}

// UpdateSchedule changes a plan; days already planned stay, later ones follow the new plan.
func (s *Service) UpdateSchedule(ctx context.Context, id uuid.UUID, in ScheduleInput) (*ent.ServiceSchedule, error) {
	sc, err := s.client.ServiceSchedule.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := in.check(); err != nil {
		return nil, err
	}
	u := sc.Update().SetName(in.Name).SetFrequency(serviceschedule.Frequency(in.Frequency)).SetDays(in.Days).
		SetTimeWindow(in.TimeWindow).SetZone(in.Zone).SetChecklist(in.Checklist)
	if in.Active != nil {
		u.SetActive(*in.Active)
	}
	return u.Save(ctx)
}

// ScheduleProperty returns the property of a visit plan (for scope checks).
func (s *Service) ScheduleProperty(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	sc, err := s.client.ServiceSchedule.Query().Where(serviceschedule.ID(id)).Select(serviceschedule.FieldPropertyID).Only(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	return sc.PropertyID, nil
}

// ContractSchedules lists a contract's visit plans.
func (s *Service) ContractSchedules(ctx context.Context, contractID uuid.UUID) ([]*ent.ServiceSchedule, error) {
	return s.client.ServiceSchedule.Query().Where(serviceschedule.ContractID(contractID)).
		Order(ent.Asc(serviceschedule.FieldName)).All(ctx)
}

// visitDays returns the days of a plan from start to end (dates at midnight UTC).
func visitDays(sc *ent.ServiceSchedule, from, to time.Time) []time.Time {
	var out []time.Time
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		var ok bool
		switch sc.Frequency {
		case serviceschedule.FrequencyDaily:
			ok = true
		case serviceschedule.FrequencyWeekly:
			ok = slices.Contains(sc.Days, int(d.Weekday()))
		case serviceschedule.FrequencyMonthly:
			ok = slices.Contains(sc.Days, d.Day())
		case serviceschedule.FrequencyQuarterly:
			ok = (d.Month()-1)%3 == 0 && slices.Contains(sc.Days, d.Day())
		case serviceschedule.FrequencyHalfYearly:
			ok = (d.Month()-1)%6 == 0 && slices.Contains(sc.Days, d.Day())
		}
		if ok {
			out = append(out, d)
		}
	}
	return out
}

// PlanVisits is the daily visits job: every active plan on an active contract gets its visits for
// the coming week (each day planned once, tracked by metadata.generated_through), and visits whose
// day has passed while still scheduled are marked missed, which feeds service credits and the
// vendor's coverage. Returns how many were planned and missed.
func (s *Service) PlanVisits(ctx context.Context, tenants []uuid.UUID) (planned, missed int, err error) {
	if len(tenants) == 0 {
		return 0, 0, nil
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	horizon := today.AddDate(0, 0, visitHorizon)
	plans, err := s.client.ServiceSchedule.Query().Where(serviceschedule.TenantIDIn(tenants...), serviceschedule.Active(true)).
		Limit(2000).All(ctx)
	if err != nil {
		return 0, 0, err
	}
	contractIDs := make([]uuid.UUID, 0, len(plans))
	for _, p := range plans {
		contractIDs = append(contractIDs, p.ContractID)
	}
	contracts, err := s.client.VendorContract.Query().Where(vendorcontract.IDIn(contractIDs...),
		vendorcontract.StatusEQ(vendorcontract.StatusActive)).All(ctx)
	if err != nil {
		return 0, 0, err
	}
	live := make(map[uuid.UUID]*ent.VendorContract, len(contracts))
	for _, c := range contracts {
		live[c.ID] = c
	}
	for _, p := range plans {
		c := live[p.ContractID]
		if c == nil {
			continue
		}
		from := today
		if g, _ := p.Metadata["generated_through"].(string); g != "" {
			if t, err := time.Parse("2006-01-02", g); err == nil && !t.Before(today) {
				from = t.AddDate(0, 0, 1)
			}
		}
		if from.After(horizon) {
			continue
		}
		to := horizon
		if c.EndsOn != nil && c.EndsOn.Before(to) {
			to = c.EndsOn.UTC().Truncate(24 * time.Hour)
		}
		tctx := tenantguard.With(ctx, p.TenantID)
		days := visitDays(p, from, to)
		meta := maps.Clone(p.Metadata)
		if meta == nil {
			meta = map[string]any{}
		}
		meta["generated_through"] = horizon.Format("2006-01-02")
		tx, terr := s.client.Tx(tctx)
		if terr != nil {
			return planned, missed, terr
		}
		if len(days) > 0 {
			bulk := make([]*ent.ServiceVisitCreate, len(days))
			for i, d := range days {
				bulk[i] = tx.ServiceVisit.Create().SetScheduleID(p.ID).SetContractID(c.ID).SetVendorID(c.VendorID).
					SetPropertyID(p.PropertyID).SetDueAt(d)
			}
			if err := tx.ServiceVisit.CreateBulk(bulk...).Exec(tctx); err != nil {
				_ = tx.Rollback()
				return planned, missed, err
			}
		}
		if err := tx.ServiceSchedule.UpdateOneID(p.ID).SetMetadata(meta).Exec(tctx); err != nil {
			_ = tx.Rollback()
			return planned, missed, err
		}
		if err := tx.Commit(); err != nil {
			return planned, missed, err
		}
		planned += len(days)
	}
	n, err := s.client.ServiceVisit.Update().Where(servicevisit.TenantIDIn(tenants...), servicevisit.StatusEQ(servicevisit.StatusScheduled),
		servicevisit.DueAtLT(today)).SetStatus(servicevisit.StatusMissed).Save(ctx)
	return planned, n, err
}

// VisitInput records a visit's evidence: completed or partial, photos and the checklist answers.
type VisitInput struct {
	Status    string           `json:"status"` // completed, partial
	Photos    []string         `json:"photos"`
	Checklist []map[string]any `json:"checklist_result"`
}

// RecordVisit records what was done on a visit (staff confirming, or the vendor's supervisor).
func (s *Service) RecordVisit(ctx context.Context, id uuid.UUID, in VisitInput) (*ent.ServiceVisit, error) {
	v, err := s.client.ServiceVisit.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	st := servicevisit.Status(in.Status)
	if st != servicevisit.StatusCompleted && st != servicevisit.StatusPartial {
		return nil, httpx.Invalid("a visit is completed or partial")
	}
	if v.Status == servicevisit.StatusCompleted {
		return nil, httpx.Conflict("this visit is already recorded as completed")
	}
	if len(in.Photos) > 10 {
		return nil, httpx.Invalid("at most 10 photos per visit")
	}
	now := time.Now()
	u := v.Update().SetStatus(st).SetCheckedOutAt(now).SetPhotos(in.Photos).SetChecklistResult(in.Checklist)
	if v.CheckedInAt == nil {
		u.SetCheckedInAt(now)
	}
	return u.Save(ctx)
}

// VisitProperty returns a visit's property (for scope checks).
func (s *Service) VisitProperty(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	v, err := s.client.ServiceVisit.Query().Where(servicevisit.ID(id)).Select(servicevisit.FieldPropertyID).Only(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	return v.PropertyID, nil
}

// VisitFilter narrows the visit list: a property and a date range of at most 62 days.
type VisitFilter struct {
	PropertyID uuid.UUID
	ContractID *uuid.UUID
	Status     string
	From, To   time.Time
}

// Visits lists visits by due date (the (tenant_id, property_id, due_at) index), at most 1,000.
func (s *Service) Visits(ctx context.Context, f VisitFilter) ([]*ent.ServiceVisit, error) {
	if f.To.Before(f.From) || f.To.Sub(f.From) > 62*24*time.Hour {
		return nil, httpx.Invalid("choose a date range of at most 62 days")
	}
	q := s.client.ServiceVisit.Query().Where(servicevisit.PropertyID(f.PropertyID), servicevisit.DueAtGTE(f.From),
		servicevisit.DueAtLT(f.To.AddDate(0, 0, 1)))
	if f.ContractID != nil {
		q = q.Where(servicevisit.ContractID(*f.ContractID))
	}
	if f.Status != "" {
		q = q.Where(servicevisit.StatusEQ(servicevisit.Status(f.Status)))
	}
	return q.Order(ent.Asc(servicevisit.FieldDueAt)).Limit(1000).All(ctx)
}
