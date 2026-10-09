package settings

import (
	"context"
	"sort"

	sharedcache "github.com/Bengo-Hub/cache"
	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/property"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// Modules by property, the way POS scopes them by outlet: the tenant's switches (within its plan)
// are the ceiling, and each property narrows them with its own use case (the preset's modules) and
// its overrides (true adds back a module the tenant has, false removes one). So an estate company
// can run a gated estate with gate and water next to a sales-only development without either
// property showing the other's screens.

// propertyModuleCache holds each property's preset and overrides; the tenant set is applied on
// read so a tenant switch takes effect within its own TTL.
var propertyModuleCache = sharedcache.NewLocal[uuid.UUID, propertyModuleRule](5000, moduleCacheTTL)

type propertyModuleRule struct {
	useCase   string
	overrides map[string]bool
}

// effective applies a property's rule to the tenant's module set.
func (r propertyModuleRule) effective(tenant map[string]bool) map[string]bool {
	base, ok := Presets[r.useCase]
	out := map[string]bool{}
	if !ok {
		for m := range tenant {
			out[m] = true
		}
	} else {
		for _, m := range base {
			out[m] = true
		}
	}
	for m, on := range r.overrides {
		out[m] = on
	}
	// A module needs what it depends on; whatever the property asks for, the tenant's set caps it.
	for m, on := range out {
		if !on {
			delete(out, m)
			continue
		}
		for _, d := range ModuleDependencies[m] {
			out[d] = true
		}
	}
	for m := range out {
		if !tenant[m] {
			delete(out, m)
		}
	}
	return out
}

// PropertyModules returns the modules switched on at one property: the tenant's set narrowed by the
// property's use case and overrides. An unknown property falls back to the tenant's set.
func (s *Service) PropertyModules(ctx context.Context, tenantID, propertyID uuid.UUID) (map[string]bool, error) {
	tenant, err := s.Modules(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	rule, ok := propertyModuleCache.Get(propertyID)
	if !ok {
		p, err := s.client.Property.Query().Where(property.ID(propertyID)).
			Select(property.FieldUseCase, property.FieldModuleOverrides).Only(tenantguard.With(ctx, tenantID))
		if ent.IsNotFound(err) {
			return tenant, nil
		}
		if err != nil {
			return nil, err
		}
		rule = propertyModuleRule{useCase: p.UseCase, overrides: p.ModuleOverrides}
		propertyModuleCache.Set(propertyID, rule)
	}
	return rule.effective(tenant), nil
}

// PropertyModuleMap lists, for /auth/me, every property whose modules differ from the tenant's
// set, with its sorted module list (at most 500 properties, one query). Screens use the tenant
// list for any property not named here.
func (s *Service) PropertyModuleMap(ctx context.Context, tenantID uuid.UUID) map[string][]string {
	out := map[string][]string{}
	tenant, err := s.Modules(ctx, tenantID)
	if err != nil {
		return out
	}
	rows, err := s.client.Property.Query().Where(property.StatusEQ(property.StatusActive)).
		Select(property.FieldID, property.FieldUseCase, property.FieldModuleOverrides).Limit(500).
		All(tenantguard.With(ctx, tenantID))
	if err != nil {
		return out
	}
	for _, p := range rows {
		rule := propertyModuleRule{useCase: p.UseCase, overrides: p.ModuleOverrides}
		propertyModuleCache.Set(p.ID, rule)
		eff := rule.effective(tenant)
		if len(eff) == len(tenant) {
			continue // a subset of equal size is the same set
		}
		list := make([]string, 0, len(eff))
		for m := range eff {
			list = append(list, m)
		}
		sort.Strings(list)
		out[p.ID.String()] = list
	}
	return out
}

// ForgetProperty drops a property's cached rule after its use case or overrides change. Other
// pods catch up within moduleCacheTTL.
func (s *Service) ForgetProperty(propertyID uuid.UUID) {
	propertyModuleCache.Delete(propertyID)
}

