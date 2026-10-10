package settings

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/catalogentry"
	"github.com/bengobox/maskani-api/internal/ent/chargetype"
	"github.com/bengobox/maskani-api/internal/ent/fund"
	"github.com/bengobox/maskani-api/internal/ent/tenantmodule"
	"github.com/bengobox/maskani-api/internal/ent/tenantsetting"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// moduleCacheTTL bounds cross-pod staleness after a module switch on another pod.
const moduleCacheTTL = 60 * time.Second

// Service manages tenant settings, modules and catalogues.
type Service struct {
	client *ent.Client
	log    *zap.Logger

	mu      sync.RWMutex
	modules map[uuid.UUID]moduleEntry
}

type moduleEntry struct {
	set map[string]bool
	at  time.Time
}

// NewService creates the settings service.
func NewService(client *ent.Client, log *zap.Logger) *Service {
	return &Service{client: client, log: log.Named("settings"), modules: map[uuid.UUID]moduleEntry{}}
}

// SeedPlatformCatalog upserts the platform default catalogue entries (no tenant). Idempotent.
func (s *Service) SeedPlatformCatalog(ctx context.Context) error {
	existing, err := s.client.CatalogEntry.Query().Where(catalogentry.TenantIDIsNil()).All(ctx)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, e := range existing {
		have[e.Kind+"/"+e.Code] = true
	}
	var bulk []*ent.CatalogEntryCreate
	for i, d := range CatalogDefaults {
		if have[d.Kind+"/"+d.Code] {
			continue
		}
		c := s.client.CatalogEntry.Create().SetKind(d.Kind).SetCode(d.Code).SetName(d.Name).SetSort(i)
		if d.Attrs != nil {
			c.SetAttrs(d.Attrs)
		}
		bulk = append(bulk, c)
	}
	if len(bulk) == 0 {
		return nil
	}
	return s.client.CatalogEntry.CreateBulk(bulk...).Exec(ctx)
}

// EnsureTenantDefaults creates settings, module switches, funds and the preset's charge catalogue for
// a tenant the first time it is seen. Idempotent: existing rows are never overwritten.
func (s *Service) EnsureTenantDefaults(ctx context.Context, tenantID uuid.UUID) error {
	ctx = tenantguard.With(ctx, tenantID)
	existing, err := s.client.TenantSetting.Query().Where(tenantsetting.TenantID(tenantID)).Only(ctx)
	preset := "estate_developer"
	switch {
	case ent.IsNotFound(err):
		if err := s.client.TenantSetting.Create().SetTenantID(tenantID).SetUseCasePreset(preset).
			OnConflictColumns(tenantsetting.FieldTenantID).DoNothing().Exec(ctx); err != nil && !isNoRows(err) {
			return fmt.Errorf("settings: create: %w", err)
		}
	case err != nil:
		return err
	default:
		preset = existing.UseCasePreset
	}

	if n, _ := s.client.TenantModule.Query().Where(tenantmodule.TenantID(tenantID)).Count(ctx); n == 0 {
		if err := s.applyPreset(ctx, tenantID, preset); err != nil {
			return err
		}
	} else if err := s.carryProvidersWithMaintenance(ctx, tenantID); err != nil {
		return err
	}

	for _, f := range FundDefaults {
		if !contains(f.Presets, preset) {
			continue
		}
		err := s.client.Fund.Create().SetTenantID(tenantID).SetCode(f.Code).SetName(f.Name).
			SetKind(fund.Kind(f.Kind)).SetAccountPrefix(f.Prefix).SetIsDefault(f.Default).
			OnConflictColumns(fund.FieldTenantID, fund.FieldCode).DoNothing().Exec(ctx)
		if err != nil && !isNoRows(err) {
			return fmt.Errorf("settings: fund %s: %w", f.Code, err)
		}
	}

	for i, c := range ChargeDefaults {
		if !contains(c.Presets, preset) {
			continue
		}
		if err := s.EnableCharge(ctx, tenantID, c.Code, i); err != nil {
			return err
		}
	}
	s.invalidate(tenantID)
	return nil
}

// carryProvidersWithMaintenance keeps vendor access for tenants set up before vendors moved from the
// maintenance module to providers: when maintenance is on and providers was never switched by a
// person (no changed_by), providers is switched on. A deliberate "providers off" is respected.
func (s *Service) carryProvidersWithMaintenance(ctx context.Context, tenantID uuid.UUID) error {
	mods, err := s.client.TenantModule.Query().
		Where(tenantmodule.TenantID(tenantID), tenantmodule.ModuleIn(ModMaintenance, ModProviders)).All(ctx)
	if err != nil {
		return err
	}
	var maint, prov *ent.TenantModule
	for _, m := range mods {
		switch m.Module {
		case ModMaintenance:
			maint = m
		case ModProviders:
			prov = m
		}
	}
	if maint == nil || !maint.Enabled || (prov != nil && (prov.Enabled || prov.ChangedBy != nil)) {
		return nil
	}
	err = s.client.TenantModule.Create().SetTenantID(tenantID).SetModule(ModProviders).SetEnabled(true).
		SetEnabledAt(time.Now()).OnConflictColumns(tenantmodule.FieldTenantID, tenantmodule.FieldModule).
		UpdateEnabled().UpdateEnabledAt().Exec(ctx)
	if err != nil && !isNoRows(err) {
		return fmt.Errorf("settings: providers backfill: %w", err)
	}
	return nil
}

// EnableCharge copies a platform charge default into the tenant's catalogue. No-op if present.
func (s *Service) EnableCharge(ctx context.Context, tenantID uuid.UUID, code string, sort int) error {
	var def *ChargeDefault
	for i := range ChargeDefaults {
		if ChargeDefaults[i].Code == code {
			def = &ChargeDefaults[i]
		}
	}
	if def == nil {
		return fmt.Errorf("unknown charge %q", code)
	}
	err := s.client.ChargeType.Create().SetTenantID(tenantID).
		SetCode(def.Code).SetName(def.Name).
		SetChargeGroup(chargetype.ChargeGroup(def.Group)).SetBasis(chargetype.Basis(def.Basis)).
		SetFrequency(chargetype.Frequency(def.Frequency)).SetBillTo(chargetype.BillTo(def.BillTo)).
		SetReassignable(def.Reassignable).SetFundCode(def.Fund).SetAllocationPriority(def.Priority).
		SetTariffKind(chargetype.TariffKind(def.TariffKind)).SetSeededFrom(def.Code).SetSort(sort).
		OnConflictColumns(chargetype.FieldTenantID, chargetype.FieldCode).DoNothing().Exec(ctx)
	if err != nil && !isNoRows(err) {
		return fmt.Errorf("settings: charge %s: %w", code, err)
	}
	return nil
}

func (s *Service) applyPreset(ctx context.Context, tenantID uuid.UUID, preset string) error {
	mods, ok := Presets[preset]
	if !ok {
		return fmt.Errorf("unknown preset %q", preset)
	}
	return s.SetModules(ctx, tenantID, mods, uuid.Nil)
}

// SetModules makes exactly the given modules enabled (plus their dependencies). Disabled modules
// keep their data; only their routes, jobs and messages stop.
func (s *Service) SetModules(ctx context.Context, tenantID uuid.UUID, enabled []string, actor uuid.UUID) error {
	want := map[string]bool{}
	var add func(string)
	add = func(m string) {
		if want[m] {
			return
		}
		want[m] = true
		for _, d := range ModuleDependencies[m] {
			add(d)
		}
	}
	for _, m := range enabled {
		add(m)
	}
	want[ModProperties] = true
	now := time.Now()
	all := map[string]bool{}
	for m := range ModuleDependencies {
		all[m] = true
	}
	for m := range want {
		all[m] = true
	}
	for m := range all {
		on := want[m]
		c := s.client.TenantModule.Create().SetTenantID(tenantID).SetModule(m).SetEnabled(on)
		if on {
			c.SetEnabledAt(now)
		} else {
			c.SetDisabledAt(now)
		}
		if actor != uuid.Nil {
			c.SetChangedBy(actor)
		}
		if err := c.OnConflictColumns(tenantmodule.FieldTenantID, tenantmodule.FieldModule).
			UpdateEnabled().UpdateEnabledAt().UpdateDisabledAt().UpdateChangedBy().Exec(ctx); err != nil {
			return fmt.Errorf("settings: module %s: %w", m, err)
		}
	}
	s.invalidate(tenantID)
	return nil
}

// ApplyPreset switches a tenant to a use case preset.
func (s *Service) ApplyPreset(ctx context.Context, tenantID uuid.UUID, preset string, actor uuid.UUID) error {
	if _, ok := Presets[preset]; !ok {
		return fmt.Errorf("unknown preset %q", preset)
	}
	if err := s.client.TenantSetting.Update().Where(tenantsetting.TenantID(tenantID)).
		SetUseCasePreset(preset).Exec(ctx); err != nil {
		return err
	}
	return s.SetModules(ctx, tenantID, Presets[preset], actor)
}

// TenantsWithModule returns the tenants that have any of these modules switched on, for background
// jobs: a switched-off module is read only (FR-09), so its alerts, sends and invoicing stop with it.
// ctx must be a system context. Plan coverage is not checked here; it lives in each user's token.
func (s *Service) TenantsWithModule(ctx context.Context, modules ...string) ([]uuid.UUID, error) {
	released := make([]string, 0, len(modules))
	for _, m := range modules {
		if ReleasedModules[m] {
			released = append(released, m)
		}
	}
	if len(released) == 0 {
		return nil, nil
	}
	var rows []struct {
		TenantID uuid.UUID `json:"tenant_id"`
	}
	if err := s.client.TenantModule.Query().Where(tenantmodule.ModuleIn(released...), tenantmodule.Enabled(true)).
		Unique(true).Select(tenantmodule.FieldTenantID).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.TenantID
	}
	return ids, nil
}

// Modules returns the tenant's enabled, released modules (cached).
func (s *Service) Modules(ctx context.Context, tenantID uuid.UUID) (map[string]bool, error) {
	s.mu.RLock()
	e, ok := s.modules[tenantID]
	s.mu.RUnlock()
	if ok && time.Since(e.at) < moduleCacheTTL {
		return e.set, nil
	}
	rows, err := s.client.TenantModule.Query().
		Where(tenantmodule.TenantID(tenantID), tenantmodule.Enabled(true)).All(tenantguard.With(ctx, tenantID))
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, r := range rows {
		if ReleasedModules[r.Module] {
			set[r.Module] = true
		}
	}
	s.mu.Lock()
	s.modules[tenantID] = moduleEntry{set: set, at: time.Now()}
	s.mu.Unlock()
	return set, nil
}

// ModuleList returns the enabled modules sorted, for /auth/me.
func (s *Service) ModuleList(ctx context.Context, tenantID uuid.UUID) []string {
	set, err := s.Modules(ctx, tenantID)
	if err != nil {
		return []string{}
	}
	out := make([]string, 0, len(set))
	for m := range set {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// Get returns the tenant's settings.
func (s *Service) Get(ctx context.Context, tenantID uuid.UUID) (*ent.TenantSetting, error) {
	return s.client.TenantSetting.Query().Where(tenantsetting.TenantID(tenantID)).Only(ctx)
}

// UpdateInput carries editable settings.
type UpdateInput struct {
	TenantType         *string  `json:"tenant_type"`
	BillingDay         *int     `json:"billing_day"`
	DueDay             *int     `json:"due_day"`
	ReadingWindowStart *int     `json:"reading_window_start"`
	ReadingWindowEnd   *int     `json:"reading_window_end"`
	QuietHoursStart    *string  `json:"quiet_hours_start"`
	QuietHoursEnd      *string  `json:"quiet_hours_end"`
	AllocationOrder    *string  `json:"allocation_order"`
	BillToRevertDays   *int     `json:"bill_to_revert_days"`
	WaterLossAlertPct  *float64 `json:"water_loss_alert_pct"`
	TermsVersion       *string  `json:"terms_version"`
	PrivacyVersion     *string  `json:"privacy_version"`
	PortalSupportPhone *string  `json:"portal_support_phone"`
	PortalSupportEmail *string  `json:"portal_support_email"`
	// WalkInPolicy is kept in metadata: guard_decides (default) or ask_host.
	WalkInPolicy *string `json:"walk_in_policy"`
	// ArrearsSteps replaces the collections ladder; an empty list goes back to the default.
	ArrearsSteps *[]ArrearsStep `json:"arrears_steps"`
}

// Walk-in policies: by default the guard decides at the gate and the host is told who came in; an
// estate can require the host's answer before a walk-in is let in.
const (
	WalkInGuardDecides = "guard_decides"
	WalkInAskHost      = "ask_host"
)

// WalkInPolicy reads the estate's walk-in policy from its settings.
func WalkInPolicy(st *ent.TenantSetting) string {
	if st != nil {
		if v, _ := st.Metadata["walk_in_policy"].(string); v == WalkInAskHost {
			return WalkInAskHost
		}
	}
	return WalkInGuardDecides
}

// Update applies settings changes with range checks.
func (s *Service) Update(ctx context.Context, tenantID uuid.UUID, in UpdateInput) (*ent.TenantSetting, error) {
	cur, err := s.Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	u := cur.Update()
	day := func(v *int, set func(int) *ent.TenantSettingUpdateOne) error {
		if v == nil {
			return nil
		}
		if *v < 1 || *v > 28 {
			return fmt.Errorf("days must be between 1 and 28")
		}
		set(*v)
		return nil
	}
	for _, f := range []struct {
		v   *int
		set func(int) *ent.TenantSettingUpdateOne
	}{{in.BillingDay, u.SetBillingDay}, {in.DueDay, u.SetDueDay}, {in.ReadingWindowStart, u.SetReadingWindowStart}, {in.ReadingWindowEnd, u.SetReadingWindowEnd}} {
		if err := day(f.v, f.set); err != nil {
			return nil, err
		}
	}
	if in.TenantType != nil {
		u.SetTenantType(tenantsetting.TenantType(*in.TenantType))
	}
	if in.QuietHoursStart != nil {
		u.SetQuietHoursStart(*in.QuietHoursStart)
	}
	if in.QuietHoursEnd != nil {
		u.SetQuietHoursEnd(*in.QuietHoursEnd)
	}
	if in.AllocationOrder != nil {
		u.SetAllocationOrder(tenantsetting.AllocationOrder(*in.AllocationOrder))
	}
	if in.BillToRevertDays != nil {
		u.SetBillToRevertDays(*in.BillToRevertDays)
	}
	if in.WaterLossAlertPct != nil {
		u.SetWaterLossAlertPct(*in.WaterLossAlertPct)
	}
	if in.TermsVersion != nil {
		u.SetTermsVersion(*in.TermsVersion)
	}
	if in.PrivacyVersion != nil {
		u.SetPrivacyVersion(*in.PrivacyVersion)
	}
	if in.PortalSupportPhone != nil {
		u.SetPortalSupportPhone(*in.PortalSupportPhone)
	}
	if in.PortalSupportEmail != nil {
		u.SetPortalSupportEmail(*in.PortalSupportEmail)
	}
	if in.ArrearsSteps != nil {
		if err := validateArrears(*in.ArrearsSteps); err != nil {
			return nil, err
		}
		u.SetArrearsSteps(arrearsJSON(*in.ArrearsSteps))
	}
	if in.WalkInPolicy != nil {
		if *in.WalkInPolicy != WalkInGuardDecides && *in.WalkInPolicy != WalkInAskHost {
			return nil, fmt.Errorf("walk-in policy must be guard_decides or ask_host")
		}
		meta := map[string]any{}
		for k, v := range cur.Metadata {
			meta[k] = v
		}
		meta["walk_in_policy"] = *in.WalkInPolicy
		u.SetMetadata(meta)
	}
	return u.Save(ctx)
}

// Catalogue returns the merged catalogue for a kind: tenant overrides replace defaults by code.
func (s *Service) Catalogue(ctx context.Context, tenantID uuid.UUID, kind string) ([]*ent.CatalogEntry, error) {
	rows, err := s.client.CatalogEntry.Query().
		Where(catalogentry.Kind(kind), catalogentry.Or(catalogentry.TenantIDIsNil(), catalogentry.TenantID(tenantID))).
		Order(ent.Asc(catalogentry.FieldSort), ent.Asc(catalogentry.FieldName)).All(ctx)
	if err != nil {
		return nil, err
	}
	byCode := map[string]*ent.CatalogEntry{}
	order := []string{}
	for _, r := range rows {
		prev, seen := byCode[r.Code]
		if !seen {
			order = append(order, r.Code)
		}
		if !seen || (prev.TenantID == nil && r.TenantID != nil) {
			byCode[r.Code] = r
		}
	}
	out := make([]*ent.CatalogEntry, 0, len(order))
	for _, c := range order {
		if byCode[c].Active {
			out = append(out, byCode[c])
		}
	}
	return out, nil
}

// UpsertCatalogEntry creates or replaces a tenant override or custom entry.
func (s *Service) UpsertCatalogEntry(ctx context.Context, tenantID uuid.UUID, kind, code, name string, active bool, attrs map[string]any) error {
	c := s.client.CatalogEntry.Create().SetTenantID(tenantID).SetKind(kind).SetCode(code).SetName(name).SetActive(active)
	if attrs != nil {
		c.SetAttrs(attrs)
	}
	existing, err := s.client.CatalogEntry.Query().
		Where(catalogentry.TenantID(tenantID), catalogentry.Kind(kind), catalogentry.Code(code)).Only(ctx)
	if err == nil {
		u := existing.Update().SetName(name).SetActive(active)
		if attrs != nil {
			u.SetAttrs(attrs)
		}
		return u.Exec(ctx)
	}
	if !ent.IsNotFound(err) {
		return err
	}
	return c.Exec(ctx)
}

func (s *Service) invalidate(tenantID uuid.UUID) {
	s.mu.Lock()
	delete(s.modules, tenantID)
	s.mu.Unlock()
}

// Ent returns sql.ErrNoRows from Exec after DoNothing on conflict for some drivers.
func isNoRows(err error) bool {
	return err != nil && err.Error() == "sql: no rows in result set"
}
