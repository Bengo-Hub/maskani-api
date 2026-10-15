package works

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/maintenanceschedule"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// preventiveLead is how far ahead a preventive work order is raised before its due date, so it is
// planned and assigned in time.
const preventiveLead = 7 * 24 * time.Hour

// MaintenanceInput is a preventive maintenance plan for a shared asset (lift, pump, generator).
type MaintenanceInput struct {
	PropertyID      uuid.UUID  `json:"property_id"`
	AssetRef        string     `json:"asset_ref"`
	Title           string     `json:"title"`
	Category        string     `json:"category"`
	Frequency       string     `json:"frequency"` // weekly, monthly, quarterly, half_yearly, annual
	NextDueAt       time.Time  `json:"next_due_at"`
	DefaultVendorID *uuid.UUID `json:"default_vendor_id"`
	Active          *bool      `json:"active"`
}

func (in *MaintenanceInput) check() error {
	in.Title, in.Category = strings.TrimSpace(in.Title), strings.TrimSpace(in.Category)
	switch {
	case in.PropertyID == uuid.Nil:
		return httpx.Invalid("choose the property")
	case in.Title == "" || in.Category == "":
		return httpx.Invalid("give the task a title and a category")
	case maintenanceschedule.FrequencyValidator(maintenanceschedule.Frequency(in.Frequency)) != nil:
		return httpx.Invalid("frequency must be weekly, monthly, quarterly, half_yearly or annual")
	case in.NextDueAt.IsZero():
		return httpx.Invalid("give the date it is next due")
	}
	return nil
}

// CreateMaintenance adds a preventive maintenance plan.
func (s *Service) CreateMaintenance(ctx context.Context, in MaintenanceInput) (*ent.MaintenanceSchedule, error) {
	if err := in.check(); err != nil {
		return nil, err
	}
	return s.client.MaintenanceSchedule.Create().SetPropertyID(in.PropertyID).SetAssetRef(in.AssetRef).SetTitle(in.Title).
		SetCategory(in.Category).SetFrequency(maintenanceschedule.Frequency(in.Frequency)).SetNextDueAt(in.NextDueAt).
		SetNillableDefaultVendorID(in.DefaultVendorID).Save(ctx)
}

// UpdateMaintenance changes a plan, or pauses and resumes it (active).
func (s *Service) UpdateMaintenance(ctx context.Context, id uuid.UUID, in MaintenanceInput) (*ent.MaintenanceSchedule, error) {
	m, err := s.client.MaintenanceSchedule.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	in.PropertyID = m.PropertyID
	if err := in.check(); err != nil {
		return nil, err
	}
	u := m.Update().SetAssetRef(in.AssetRef).SetTitle(in.Title).SetCategory(in.Category).
		SetFrequency(maintenanceschedule.Frequency(in.Frequency)).SetNextDueAt(in.NextDueAt)
	if in.DefaultVendorID != nil {
		u.SetDefaultVendorID(*in.DefaultVendorID)
	} else {
		u.ClearDefaultVendorID()
	}
	if in.Active != nil {
		u.SetActive(*in.Active)
	}
	return u.Save(ctx)
}

// MaintenanceProperty returns the property of a plan (for scope checks).
func (s *Service) MaintenanceProperty(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	m, err := s.client.MaintenanceSchedule.Query().Where(maintenanceschedule.ID(id)).Select(maintenanceschedule.FieldPropertyID).Only(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	return m.PropertyID, nil
}

// MaintenancePlans lists the plans of the given properties (all when all), soonest due first.
func (s *Service) MaintenancePlans(ctx context.Context, propertyIDs []uuid.UUID, all bool) ([]*ent.MaintenanceSchedule, error) {
	q := s.client.MaintenanceSchedule.Query()
	if !all {
		q = q.Where(maintenanceschedule.PropertyIDIn(propertyIDs...))
	}
	return q.Order(ent.Asc(maintenanceschedule.FieldNextDueAt)).Limit(500).All(ctx)
}

// nextDue moves a due date on by one period of the plan's frequency.
func nextDue(t time.Time, f maintenanceschedule.Frequency) time.Time {
	switch f {
	case maintenanceschedule.FrequencyWeekly:
		return t.AddDate(0, 0, 7)
	case maintenanceschedule.FrequencyMonthly:
		return t.AddDate(0, 1, 0)
	case maintenanceschedule.FrequencyQuarterly:
		return t.AddDate(0, 3, 0)
	case maintenanceschedule.FrequencyHalfYearly:
		return t.AddDate(0, 6, 0)
	}
	return t.AddDate(1, 0, 0)
}

// RaisePreventive is the daily preventive maintenance job: each active plan due within the lead
// time gets a work order (source schedule, assigned to its default vendor when set) and moves on by
// one period. A plan far behind raises one work order and jumps to its next date in the future, so
// a long pause never floods the queue. Served by the (tenant_id, active, next_due_at) index.
func (s *Service) RaisePreventive(ctx context.Context, tenants []uuid.UUID) (int, error) {
	if len(tenants) == 0 {
		return 0, nil
	}
	now := time.Now()
	plans, err := s.client.MaintenanceSchedule.Query().Where(maintenanceschedule.TenantIDIn(tenants...), maintenanceschedule.Active(true),
		maintenanceschedule.NextDueAtLTE(now.Add(preventiveLead))).Order(ent.Asc(maintenanceschedule.FieldNextDueAt)).Limit(500).All(ctx)
	if err != nil {
		return 0, err
	}
	raised := 0
	for _, p := range plans {
		tctx := tenantguard.With(ctx, p.TenantID)
		pid := p.ID
		desc := "Preventive maintenance due " + p.NextDueAt.Format("2 Jan 2006") + "."
		if _, err := s.Create(tctx, Actor{Kind: "schedule"}, RequestInput{PropertyID: p.PropertyID, Category: p.Category,
			Title: p.Title, Description: desc, Priority: "normal", MaintenanceScheduleID: &pid, AssetRef: p.AssetRef,
			VendorID: p.DefaultVendorID}); err != nil {
			s.log.Warn("preventive work order not raised")
			continue
		}
		next := nextDue(p.NextDueAt, p.Frequency)
		for !next.After(now) {
			next = nextDue(next, p.Frequency)
		}
		if err := p.Update().SetNextDueAt(next).Exec(tctx); err != nil {
			return raised, err
		}
		raised++
	}
	return raised, nil
}
