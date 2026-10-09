package rbac

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/maskanipermission"
	"github.com/bengobox/maskani-api/internal/ent/maskanirole"
	"github.com/bengobox/maskani-api/internal/ent/maskaniuser"
	"github.com/bengobox/maskani-api/internal/ent/rolepermission"
	"github.com/bengobox/maskani-api/internal/ent/userroleassignment"
)

// cacheTTL bounds how stale a pod's permission view can be after a role change on another pod.
const cacheTTL = 60 * time.Second

type cached struct {
	roles []string
	perms []string
	at    time.Time
}

// Service seeds the catalogue and resolves users, roles and permissions.
type Service struct {
	client *ent.Client
	log    *zap.Logger

	mu    sync.RWMutex
	cache map[string]cached
	seen  map[string]time.Time
}

// NewService creates the RBAC service.
func NewService(client *ent.Client, log *zap.Logger) *Service {
	return &Service{client: client, log: log.Named("rbac"), cache: map[string]cached{}, seen: map[string]time.Time{}}
}

// Seed upserts the permission catalogue and system roles with their permission sets. Idempotent.
func (s *Service) Seed(ctx context.Context) error {
	existing, err := s.client.MaskaniPermission.Query().Select(maskanipermission.FieldPermissionCode).Strings(ctx)
	if err != nil {
		return fmt.Errorf("rbac seed: %w", err)
	}
	had := map[string]bool{}
	for _, c := range existing {
		had[c] = true
	}
	permIDs := map[string]uuid.UUID{}
	for _, p := range Catalogue {
		id, err := s.client.MaskaniPermission.Create().
			SetPermissionCode(p.Code).SetName(p.Name).SetModule(p.Module).SetAction(p.Action).
			OnConflictColumns(maskanipermission.FieldPermissionCode).
			UpdateName().UpdateModule().UpdateAction().
			ID(ctx)
		if err != nil {
			return fmt.Errorf("rbac seed permission %s: %w", p.Code, err)
		}
		permIDs[p.Code] = id
	}
	for _, r := range Roles {
		role, err := s.client.MaskaniRole.Query().
			Where(maskanirole.RoleCode(r.Code), maskanirole.TenantIDIsNil()).Only(ctx)
		if ent.IsNotFound(err) {
			role, err = s.client.MaskaniRole.Create().
				SetRoleCode(r.Code).SetName(r.Name).SetDescription(r.Description).
				SetIsSystemRole(true).SetIsCustomerRole(r.Customer).Save(ctx)
		} else if err == nil {
			role, err = role.Update().SetName(r.Name).SetDescription(r.Description).
				SetIsCustomerRole(r.Customer).Save(ctx)
		}
		if err != nil {
			return fmt.Errorf("rbac seed role %s: %w", r.Code, err)
		}
		if _, err := s.client.RolePermission.Delete().Where(rolepermission.RoleID(role.ID)).Exec(ctx); err != nil {
			return err
		}
		bulk := make([]*ent.RolePermissionCreate, 0, len(r.Permissions))
		for _, code := range r.Permissions {
			if pid, ok := permIDs[code]; ok {
				bulk = append(bulk, s.client.RolePermission.Create().SetRoleID(role.ID).SetPermissionID(pid))
			}
		}
		if len(bulk) > 0 {
			if err := s.client.RolePermission.CreateBulk(bulk...).Exec(ctx); err != nil {
				return fmt.Errorf("rbac seed role permissions %s: %w", r.Code, err)
			}
		}
	}
	// A code split out of an older one reaches the estates' own role copies once, on the start
	// that first creates it (a fresh database has no estate roles, so this is a no-op there).
	for code, parent := range ImpliedBy {
		if had[code] || len(had) == 0 {
			continue
		}
		if err := s.grantImplied(ctx, permIDs[code], permIDs[parent]); err != nil {
			return fmt.Errorf("rbac seed implied %s: %w", code, err)
		}
	}
	return nil
}

// grantImplied gives perm to every estate role that holds parent and lacks perm.
func (s *Service) grantImplied(ctx context.Context, perm, parent uuid.UUID) error {
	if perm == uuid.Nil || parent == uuid.Nil {
		return nil
	}
	holders, err := s.client.RolePermission.Query().Where(rolepermission.PermissionID(parent),
		rolepermission.HasRoleWith(maskanirole.TenantIDNotNil())).Select(rolepermission.FieldRoleID).Strings(ctx)
	if err != nil || len(holders) == 0 {
		return err
	}
	ids := make([]uuid.UUID, 0, len(holders))
	for _, h := range holders {
		if id, err := uuid.Parse(h); err == nil {
			ids = append(ids, id)
		}
	}
	have, err := s.client.RolePermission.Query().Where(rolepermission.PermissionID(perm), rolepermission.RoleIDIn(ids...)).
		Select(rolepermission.FieldRoleID).Strings(ctx)
	if err != nil {
		return err
	}
	skip := map[string]bool{}
	for _, h := range have {
		skip[h] = true
	}
	bulk := make([]*ent.RolePermissionCreate, 0, len(ids))
	for _, id := range ids {
		if !skip[id.String()] {
			bulk = append(bulk, s.client.RolePermission.Create().SetRoleID(id).SetPermissionID(perm))
		}
	}
	if len(bulk) == 0 {
		return nil
	}
	// Two pods starting together both try; the (role, permission) key keeps one row.
	return s.client.RolePermission.CreateBulk(bulk...).
		OnConflictColumns(rolepermission.FieldRoleID, rolepermission.FieldPermissionID).DoNothing().Exec(ctx)
}

// Identity is what the token and the request tell us about a caller.
type Identity struct {
	TenantID   uuid.UUID
	AuthUserID uuid.UUID
	Email      string
	Name       string
	Phone      string
	SSORoles   []string
	// Bypass is a platform owner, superuser or service caller.
	Bypass bool
	// OutletUseCase is the token's outlet use case, when it was minted for an outlet.
	OutletUseCase string
	// HasParty is true when the user is linked to an estate party (an owner or resident).
	HasParty bool
}

// IsPropertyUseCase reports whether a tenant or outlet use case is a Maskani property one. It is the
// single list: the outlet projection (tenant.SyncOutlets) and user admission both use it.
func IsPropertyUseCase(v string) bool {
	switch v {
	case "property", "estate", "real_estate", "maskani":
		return true
	}
	return false
}

// EnsureUser creates or refreshes the local user and, when the user has no role yet, assigns the
// role mapped from their SSO roles. Throttled per pod so it does not write on every request.
//
// Only people who belong to property work get a local user: platform owners, tenant admins,
// property-specific roles, tokens minted for a property outlet, linked estate parties, and staff a
// Maskani admin has already given a role. Everyone else in a multi-product tenant (a POS cashier,
// a clinic nurse) gets nil and no permissions, so they are never listed or granted anything here.
func (s *Service) EnsureUser(ctx context.Context, id Identity) (*ent.MaskaniUser, error) {
	key := id.TenantID.String() + ":" + id.AuthUserID.String()
	s.mu.RLock()
	last, ok := s.seen[key]
	s.mu.RUnlock()
	if ok && time.Since(last) < 5*time.Minute {
		u, err := s.client.MaskaniUser.Query().
			Where(maskaniuser.TenantID(id.TenantID), maskaniuser.AuthServiceUserID(id.AuthUserID)).Only(ctx)
		if ent.IsNotFound(err) {
			return nil, nil
		}
		return u, err
	}

	propertyTenant := false
	if t, err := s.client.Tenant.Get(ctx, id.TenantID); err == nil && t.UseCase != nil {
		propertyTenant = IsPropertyUseCase(*t.UseCase)
	}
	roleCode := ""
	for _, r := range id.SSORoles {
		if m := MapSSORole(r, propertyTenant); m != "" {
			if roleCode == "" || m == RoleTenantAdmin {
				roleCode = m
			}
		}
	}

	relevant := id.Bypass || roleCode != "" || id.HasParty || IsPropertyUseCase(id.OutletUseCase)
	if !relevant {
		// Someone a Maskani admin granted a role stays; anyone else is left out, and roles the old
		// unconditional mapping granted them automatically (assigned by themselves) are removed.
		granted, err := s.adminGranted(ctx, id)
		if err != nil {
			return nil, err
		}
		if !granted {
			s.mu.Lock()
			s.seen[key] = time.Now()
			s.mu.Unlock()
			return nil, nil
		}
	}
	kind := maskaniuser.KindStaff
	switch roleCode {
	case RoleOwner, RoleOccupant:
		kind = maskaniuser.KindCustomer
	case RoleVendorSupervisor:
		kind = maskaniuser.KindVendorSupervisor
	case RoleGuard:
		kind = maskaniuser.KindGuard
	}

	create := s.client.MaskaniUser.Create().
		SetTenantID(id.TenantID).SetAuthServiceUserID(id.AuthUserID).SetKind(kind).
		SetSyncStatus("synced").SetLastSyncAt(time.Now())
	if id.Email != "" {
		create.SetEmail(id.Email)
	}
	if id.Name != "" {
		create.SetName(id.Name)
	}
	if id.Phone != "" {
		create.SetPhone(id.Phone)
	}
	userID, err := create.
		OnConflictColumns(maskaniuser.FieldTenantID, maskaniuser.FieldAuthServiceUserID).
		UpdateLastSyncAt().
		ID(ctx)
	if err != nil {
		return nil, fmt.Errorf("rbac ensure user: %w", err)
	}

	if roleCode != "" {
		has, err := s.client.UserRoleAssignment.Query().
			Where(userroleassignment.TenantID(id.TenantID), userroleassignment.UserID(userID)).Exist(ctx)
		if err == nil && !has {
			if roles, rerr := s.EffectiveRoles(ctx, id.TenantID, []string{roleCode}); rerr == nil && len(roles) == 1 {
				role := roles[0]
				_ = s.client.UserRoleAssignment.Create().
					SetTenantID(id.TenantID).SetUserID(userID).SetRoleID(role.ID).SetAssignedBy(id.AuthUserID).
					OnConflictColumns(userroleassignment.FieldTenantID, userroleassignment.FieldUserID, userroleassignment.FieldRoleID).
					DoNothing().Exec(ctx)
				s.invalidate(id.TenantID, id.AuthUserID)
			}
		}
	}

	s.mu.Lock()
	s.seen[key] = time.Now()
	s.mu.Unlock()
	return s.client.MaskaniUser.Get(ctx, userID)
}

// adminGranted reports whether an existing local user holds a role someone else assigned. Roles the
// user was granted automatically at sign-in (assigned_by is the user) are removed on the way, which
// cleans up grants made before sign-in was limited to property staff.
func (s *Service) adminGranted(ctx context.Context, id Identity) (bool, error) {
	u, err := s.client.MaskaniUser.Query().
		Where(maskaniuser.TenantID(id.TenantID), maskaniuser.AuthServiceUserID(id.AuthUserID)).Only(ctx)
	if ent.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	n, err := s.client.UserRoleAssignment.Delete().Where(userroleassignment.TenantID(id.TenantID),
		userroleassignment.UserID(u.ID), userroleassignment.AssignedBy(id.AuthUserID)).Exec(ctx)
	if err != nil {
		return false, err
	}
	if n > 0 {
		s.log.Info("removed automatic maskani roles from a user outside property work",
			zap.String("auth_user_id", id.AuthUserID.String()), zap.Int("roles", n))
		s.invalidate(id.TenantID, id.AuthUserID)
	}
	return s.client.UserRoleAssignment.Query().
		Where(userroleassignment.TenantID(id.TenantID), userroleassignment.UserID(u.ID)).Exist(ctx)
}

// Resolve returns the caller's Maskani roles and permissions within the tenant.
func (s *Service) Resolve(ctx context.Context, tenantID, authUserID uuid.UUID) (roles, perms []string, err error) {
	key := tenantID.String() + ":" + authUserID.String()
	s.mu.RLock()
	c, ok := s.cache[key]
	s.mu.RUnlock()
	if ok && time.Since(c.at) < cacheTTL {
		return c.roles, c.perms, nil
	}

	user, err := s.client.MaskaniUser.Query().
		Where(maskaniuser.TenantID(tenantID), maskaniuser.AuthServiceUserID(authUserID)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if user.Status != "active" {
		return nil, nil, nil
	}
	now := time.Now()
	assignments, err := s.client.UserRoleAssignment.Query().
		Where(userroleassignment.TenantID(tenantID), userroleassignment.UserID(user.ID),
			userroleassignment.Or(userroleassignment.ExpiresAtIsNil(), userroleassignment.ExpiresAtGT(now))).
		WithRole(func(q *ent.MaskaniRoleQuery) { q.WithPermissions() }).
		All(ctx)
	if err != nil {
		return nil, nil, err
	}
	roleSet, permSet := map[string]bool{}, map[string]bool{}
	for _, a := range assignments {
		if a.Edges.Role == nil {
			continue
		}
		roleSet[a.Edges.Role.RoleCode] = true
		for _, p := range a.Edges.Role.Edges.Permissions {
			permSet[p.PermissionCode] = true
		}
	}
	roles, perms = keys(roleSet), keys(permSet)
	s.mu.Lock()
	s.cache[key] = cached{roles: roles, perms: perms, at: time.Now()}
	s.mu.Unlock()
	return roles, perms, nil
}

// HasAny reports whether the caller holds any of the permissions.
func (s *Service) HasAny(ctx context.Context, tenantID, authUserID uuid.UUID, codes ...string) bool {
	_, perms, err := s.Resolve(ctx, tenantID, authUserID)
	if err != nil {
		s.log.Warn("permission resolve failed", zap.Error(err))
		return false
	}
	for _, c := range codes {
		for _, p := range perms {
			if p == c {
				return true
			}
		}
	}
	return false
}

// SetUserRoles replaces a user's role assignments within the tenant.
func (s *Service) SetUserRoles(ctx context.Context, tenantID, userID, actor uuid.UUID, roleCodes []string) error {
	user, err := s.client.MaskaniUser.Get(ctx, userID)
	if err != nil || user.TenantID != tenantID {
		return fmt.Errorf("user not found")
	}
	// The tenant's own copy of a role replaces the global one, so a code never assigns both.
	roles, err := s.EffectiveRoles(ctx, tenantID, roleCodes)
	if err != nil {
		return err
	}
	if len(roles) == 0 {
		return fmt.Errorf("no valid roles")
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	if _, err := tx.UserRoleAssignment.Delete().
		Where(userroleassignment.TenantID(tenantID), userroleassignment.UserID(userID)).Exec(ctx); err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, r := range roles {
		if err := tx.UserRoleAssignment.Create().SetTenantID(tenantID).SetUserID(userID).
			SetRoleID(r.ID).SetAssignedBy(actor).Exec(ctx); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.invalidate(tenantID, user.AuthServiceUserID)
	return nil
}

// UserView is a user row with role codes for the admin screen.
type UserView struct {
	*ent.MaskaniUser
	Roles []string `json:"roles"`
}

// ListUsers returns the tenant's users of a kind with their roles. Bounded by limit.
func (s *Service) ListUsers(ctx context.Context, tenantID uuid.UUID, kind, search string, limit int) ([]UserView, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	// Staff appear once they hold a role; a roleless staff row is a sign-in from outside property work
	// (or a platform owner) and is not part of the estate's team.
	q := s.client.MaskaniUser.Query().Where(maskaniuser.TenantID(tenantID),
		maskaniuser.Or(maskaniuser.HasRoleAssignments(), maskaniuser.KindNEQ(maskaniuser.KindStaff)))
	if kind != "" {
		q = q.Where(maskaniuser.KindEQ(maskaniuser.Kind(kind)))
	}
	if t := strings.TrimSpace(search); t != "" {
		q = q.Where(maskaniuser.Or(maskaniuser.NameContainsFold(t), maskaniuser.EmailContainsFold(t)))
	}
	users, err := q.WithRoleAssignments(func(a *ent.UserRoleAssignmentQuery) { a.WithRole() }).
		Order(ent.Asc(maskaniuser.FieldName)).Limit(limit).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]UserView, 0, len(users))
	for _, u := range users {
		v := UserView{MaskaniUser: u, Roles: []string{}}
		for _, a := range u.Edges.RoleAssignments {
			if a.Edges.Role != nil {
				v.Roles = append(v.Roles, a.Edges.Role.RoleCode)
			}
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Service) invalidate(tenantID, authUserID uuid.UUID) {
	s.mu.Lock()
	delete(s.cache, tenantID.String()+":"+authUserID.String())
	s.mu.Unlock()
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
