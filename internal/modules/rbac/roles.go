package rbac

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/maskanipermission"
	"github.com/bengobox/maskani-api/internal/ent/maskanirole"
	"github.com/bengobox/maskani-api/internal/ent/maskaniuser"
	"github.com/bengobox/maskani-api/internal/ent/rolepermission"
	"github.com/bengobox/maskani-api/internal/ent/userroleassignment"
)

// Roles work like hospital-api's: global system roles are the defaults; a tenant edits one by
// customising it (a tenant copy that takes the global role's place for that tenant only), or
// creates its own. Wherever a role is looked up by code, the tenant's copy wins.

// ErrRole is a refused role change, explained for the admin.
type ErrRole struct{ Msg string }

func (e *ErrRole) Error() string { return e.Msg }

var roleCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,40}$`)

// EffectiveRoles returns the roles for these codes as the tenant sees them: its own copy when it
// has one, the global role otherwise. Unknown codes are left out.
func (s *Service) EffectiveRoles(ctx context.Context, tenantID uuid.UUID, codes []string) ([]*ent.MaskaniRole, error) {
	rows, err := s.client.MaskaniRole.Query().
		Where(maskanirole.RoleCodeIn(codes...), maskanirole.Or(maskanirole.TenantIDIsNil(), maskanirole.TenantID(tenantID))).All(ctx)
	if err != nil {
		return nil, err
	}
	byCode := map[string]*ent.MaskaniRole{}
	for _, r := range rows {
		if cur, ok := byCode[r.RoleCode]; !ok || (cur.TenantID == nil && r.TenantID != nil) {
			byCode[r.RoleCode] = r
		}
	}
	out := make([]*ent.MaskaniRole, 0, len(byCode))
	for _, c := range codes {
		if r, ok := byCode[c]; ok {
			out = append(out, r)
			delete(byCode, c)
		}
	}
	return out, nil
}

// RoleView is a role as the admin screens show it: its permission codes and how many staff hold it.
type RoleView struct {
	ID          uuid.UUID  `json:"id"`
	Code        string     `json:"code"`
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	System      bool       `json:"is_system_role"`
	Customer    bool       `json:"is_customer_role"`
	Custom      bool       `json:"is_custom"`
	ClonedFrom  *uuid.UUID `json:"cloned_from_role_id,omitempty"`
	Permissions []string   `json:"permissions"`
	Holders     int        `json:"holders"`
	Locked      bool       `json:"locked"` // tenant_admin: always every permission, so nobody locks the estate out
}

// RoleViews lists the roles the tenant uses (its copies replace the global ones) with permissions
// and holder counts, in two queries.
func (s *Service) RoleViews(ctx context.Context, tenantID uuid.UUID) ([]RoleView, error) {
	rows, err := s.client.MaskaniRole.Query().
		Where(maskanirole.Or(maskanirole.TenantIDIsNil(), maskanirole.TenantID(tenantID))).
		WithPermissions().Order(ent.Asc(maskanirole.FieldName)).All(ctx)
	if err != nil {
		return nil, err
	}
	replaced := map[uuid.UUID]bool{}
	for _, r := range rows {
		if r.ClonedFromRoleID != nil {
			replaced[*r.ClonedFromRoleID] = true
		}
	}
	var counts []struct {
		RoleID uuid.UUID `json:"role_id"`
		Count  int       `json:"count"`
	}
	if err := s.client.UserRoleAssignment.Query().Where(userroleassignment.TenantID(tenantID)).
		GroupBy(userroleassignment.FieldRoleID).Aggregate(ent.Count()).Scan(ctx, &counts); err != nil {
		return nil, err
	}
	held := map[uuid.UUID]int{}
	for _, c := range counts {
		held[c.RoleID] = c.Count
	}
	out := make([]RoleView, 0, len(rows))
	for _, r := range rows {
		if r.TenantID == nil && replaced[r.ID] {
			continue
		}
		v := RoleView{ID: r.ID, Code: r.RoleCode, Name: r.Name, Description: r.Description, System: r.TenantID == nil,
			Customer: r.IsCustomerRole, Custom: r.TenantID != nil && r.ClonedFromRoleID == nil, ClonedFrom: r.ClonedFromRoleID,
			Holders: held[r.ID], Locked: r.RoleCode == RoleTenantAdmin, Permissions: make([]string, 0, len(r.Edges.Permissions))}
		for _, p := range r.Edges.Permissions {
			v.Permissions = append(v.Permissions, p.PermissionCode)
		}
		out = append(out, v)
	}
	return out, nil
}

// Permissions is the permission catalogue, grouped by module in the UI.
func (s *Service) Permissions(ctx context.Context) ([]*ent.MaskaniPermission, error) {
	return s.client.MaskaniPermission.Query().Order(ent.Asc(maskanipermission.FieldModule), ent.Asc(maskanipermission.FieldPermissionCode)).All(ctx)
}

// tenantRole loads a role the tenant owns, refusing global roles.
func (s *Service) tenantRole(ctx context.Context, tenantID, roleID uuid.UUID) (*ent.MaskaniRole, error) {
	r, err := s.client.MaskaniRole.Get(ctx, roleID)
	if err != nil {
		return nil, &ErrRole{"role not found"}
	}
	if r.TenantID == nil || *r.TenantID != tenantID {
		return nil, &ErrRole{"this is a default role; customise it first to change it for this estate"}
	}
	return r, nil
}

// CustomizeRole gives the tenant its own copy of a global role (idempotent), with the same
// permissions, and moves the tenant's holders of the global role onto the copy.
func (s *Service) CustomizeRole(ctx context.Context, tenantID uuid.UUID, code string) (*ent.MaskaniRole, error) {
	if code == RoleTenantAdmin {
		return nil, &ErrRole{"the tenant administrator role always has every permission"}
	}
	if existing, err := s.client.MaskaniRole.Query().Where(maskanirole.TenantID(tenantID), maskanirole.RoleCode(code)).Only(ctx); err == nil {
		return existing, nil
	}
	global, err := s.client.MaskaniRole.Query().Where(maskanirole.TenantIDIsNil(), maskanirole.RoleCode(code)).WithPermissions().Only(ctx)
	if err != nil {
		return nil, &ErrRole{"role not found"}
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	cp, err := tx.MaskaniRole.Create().SetTenantID(tenantID).SetRoleCode(global.RoleCode).SetName(global.Name).
		SetDescription(global.Description).SetIsCustomerRole(global.IsCustomerRole).SetClonedFromRoleID(global.ID).Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := setRolePermissions(ctx, tx, cp.ID, permissionIDs(global)); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if _, err := tx.UserRoleAssignment.Update().Where(userroleassignment.TenantID(tenantID), userroleassignment.RoleID(global.ID)).
		SetRoleID(cp.ID).Save(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.invalidateTenant(tenantID)
	return cp, nil
}

// CreateRole adds a tenant-only role with the given permissions.
func (s *Service) CreateRole(ctx context.Context, tenantID uuid.UUID, code, name, description string, perms []string) (*ent.MaskaniRole, error) {
	code = strings.ToLower(strings.TrimSpace(code))
	if !roleCodePattern.MatchString(code) {
		return nil, &ErrRole{"role code must be 2 to 41 lowercase letters, digits or underscores, starting with a letter"}
	}
	if strings.TrimSpace(name) == "" {
		return nil, &ErrRole{"role name is required"}
	}
	if taken, _ := s.client.MaskaniRole.Query().Where(maskanirole.RoleCode(code),
		maskanirole.Or(maskanirole.TenantIDIsNil(), maskanirole.TenantID(tenantID))).Exist(ctx); taken {
		return nil, &ErrRole{"a role with this code already exists"}
	}
	ids, err := s.permissionIDsFor(ctx, perms)
	if err != nil {
		return nil, err
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	r, err := tx.MaskaniRole.Create().SetTenantID(tenantID).SetRoleCode(code).SetName(strings.TrimSpace(name)).
		SetDescription(strings.TrimSpace(description)).Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := setRolePermissions(ctx, tx, r.ID, ids); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return r, tx.Commit()
}

// UpdateRole renames a tenant role and replaces its permissions.
func (s *Service) UpdateRole(ctx context.Context, tenantID, roleID uuid.UUID, name, description *string, perms []string) (*ent.MaskaniRole, error) {
	r, err := s.tenantRole(ctx, tenantID, roleID)
	if err != nil {
		return nil, err
	}
	ids, err := s.permissionIDsFor(ctx, perms)
	if err != nil {
		return nil, err
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	u := tx.MaskaniRole.UpdateOneID(r.ID)
	if name != nil && strings.TrimSpace(*name) != "" {
		u.SetName(strings.TrimSpace(*name))
	}
	if description != nil {
		u.SetDescription(strings.TrimSpace(*description))
	}
	if r, err = u.Save(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if perms != nil {
		if err := setRolePermissions(ctx, tx, r.ID, ids); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.invalidateTenant(tenantID)
	return r, nil
}

// DeleteRole removes a tenant role. A customised copy goes back to the default (its holders move
// to the global role); a role the tenant created is refused while anyone holds it.
func (s *Service) DeleteRole(ctx context.Context, tenantID, roleID uuid.UUID) error {
	r, err := s.tenantRole(ctx, tenantID, roleID)
	if err != nil {
		return err
	}
	held, err := s.client.UserRoleAssignment.Query().Where(userroleassignment.RoleID(r.ID)).Count(ctx)
	if err != nil {
		return err
	}
	if r.ClonedFromRoleID == nil && held > 0 {
		return &ErrRole{fmt.Sprintf("%d staff still hold this role; give them another role first", held)}
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	if r.ClonedFromRoleID != nil {
		if _, err := tx.UserRoleAssignment.Update().Where(userroleassignment.RoleID(r.ID)).SetRoleID(*r.ClonedFromRoleID).Save(ctx); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if _, err := tx.RolePermission.Delete().Where(rolepermission.RoleID(r.ID)).Exec(ctx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.MaskaniRole.DeleteOneID(r.ID).Exec(ctx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.invalidateTenant(tenantID)
	return nil
}

// EnsureStaffUser creates (or returns) the local user for an auth member and gives them roles,
// assigned by actor so EnsureUser treats them as admitted staff. Used by the invite flow.
func (s *Service) EnsureStaffUser(ctx context.Context, tenantID, authUserID, actor uuid.UUID, email, name string, roleCodes []string) (*ent.MaskaniUser, error) {
	create := s.client.MaskaniUser.Create().SetTenantID(tenantID).SetAuthServiceUserID(authUserID).
		SetKind(maskaniuser.KindStaff).SetSyncStatus("synced")
	if email != "" {
		create.SetEmail(email)
	}
	if name != "" {
		create.SetName(name)
	}
	id, err := create.OnConflictColumns(maskaniuser.FieldTenantID, maskaniuser.FieldAuthServiceUserID).UpdateLastSyncAt().ID(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.SetUserRoles(ctx, tenantID, id, actor, roleCodes); err != nil {
		return nil, err
	}
	return s.client.MaskaniUser.Get(ctx, id)
}

// SetUserStatus suspends or reactivates a user's Maskani access (Resolve ignores a user who is not
// active). Nobody can suspend themselves, so an estate always keeps an administrator.
func (s *Service) SetUserStatus(ctx context.Context, tenantID, userID, actorAuthID uuid.UUID, status string) (*ent.MaskaniUser, error) {
	u, err := s.client.MaskaniUser.Get(ctx, userID)
	if err != nil || u.TenantID != tenantID {
		return nil, &ErrRole{"user not found"}
	}
	if u.AuthServiceUserID == actorAuthID && status != "active" {
		return nil, &ErrRole{"you cannot suspend yourself"}
	}
	if u, err = u.Update().SetStatus(status).Save(ctx); err != nil {
		return nil, err
	}
	s.invalidate(tenantID, u.AuthServiceUserID)
	return u, nil
}

func (s *Service) permissionIDsFor(ctx context.Context, codes []string) ([]uuid.UUID, error) {
	if len(codes) == 0 {
		return nil, nil
	}
	rows, err := s.client.MaskaniPermission.Query().Where(maskanipermission.PermissionCodeIn(codes...)).All(ctx)
	if err != nil {
		return nil, err
	}
	if len(rows) != len(uniq(codes)) {
		return nil, &ErrRole{"one or more permissions are not known"}
	}
	ids := make([]uuid.UUID, len(rows))
	for i, p := range rows {
		ids[i] = p.ID
	}
	return ids, nil
}

func permissionIDs(r *ent.MaskaniRole) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(r.Edges.Permissions))
	for _, p := range r.Edges.Permissions {
		ids = append(ids, p.ID)
	}
	return ids
}

func setRolePermissions(ctx context.Context, tx *ent.Tx, roleID uuid.UUID, permIDs []uuid.UUID) error {
	if _, err := tx.RolePermission.Delete().Where(rolepermission.RoleID(roleID)).Exec(ctx); err != nil {
		return err
	}
	if len(permIDs) == 0 {
		return nil
	}
	bulk := make([]*ent.RolePermissionCreate, len(permIDs))
	for i, id := range permIDs {
		bulk[i] = tx.RolePermission.Create().SetRoleID(roleID).SetPermissionID(id)
	}
	return tx.RolePermission.CreateBulk(bulk...).Exec(ctx)
}

func uniq(v []string) []string {
	seen := map[string]bool{}
	out := v[:0:0]
	for _, s := range v {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// invalidateTenant drops this pod's cached permissions for every user of the tenant; other pods
// pick a role change up within cacheTTL.
func (s *Service) invalidateTenant(tenantID uuid.UUID) {
	prefix := tenantID.String() + ":"
	s.mu.Lock()
	for k := range s.cache {
		if strings.HasPrefix(k, prefix) {
			delete(s.cache, k)
		}
	}
	s.mu.Unlock()
}
