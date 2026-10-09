package handlers

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/rbac"
	"github.com/bengobox/maskani-api/internal/modules/register"
)

// roleErr turns a refused role change into a 400 with its explanation.
func roleErr(w http.ResponseWriter, err error) {
	var re *rbac.ErrRole
	if errors.As(err, &re) {
		httpx.Fail(w, httpx.Invalid(re.Msg))
		return
	}
	httpx.Fail(w, err)
}

// holdsAll rejects the request unless the caller already holds every permission listed, so nobody
// can hand out (or write into a role) more than they have themselves.
func holdsAll(w http.ResponseWriter, r *http.Request, perms []string) bool {
	a := access(r)
	if a.Bypass {
		return true
	}
	for _, p := range perms {
		if !a.Has(p) {
			httpx.Error(w, http.StatusForbidden, "forbidden", "you cannot grant a permission you do not hold yourself")
			return false
		}
	}
	return true
}

// canGrantRoles applies holdsAll to the permissions the given roles carry.
func (h *H) canGrantRoles(w http.ResponseWriter, r *http.Request, codes []string) bool {
	if access(r).Bypass {
		return true
	}
	perms, err := h.RBAC.RolePermissionCodes(r.Context(), access(r).TenantID, codes)
	if err != nil {
		httpx.Fail(w, err)
		return false
	}
	return holdsAll(w, r, perms)
}

// ListRoles is GET /roles: the roles this estate uses (its customised copies replace the defaults)
// with permission codes and how many staff hold each.
func (h *H) ListRoles(w http.ResponseWriter, r *http.Request) {
	rows, err := h.RBAC.RoleViews(r.Context(), access(r).TenantID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// ListPermissions is GET /permissions: the permission catalogue for the matrix.
func (h *H) ListPermissions(w http.ResponseWriter, r *http.Request) {
	rows, err := h.RBAC.Permissions(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	out := make([]map[string]string, 0, len(rows))
	for _, p := range rows {
		out = append(out, map[string]string{"code": p.PermissionCode, "name": p.Name, "module": p.Module, "action": p.Action})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

// CreateRole is POST /roles {code, name, description, permissions}.
func (h *H) CreateRole(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code        string   `json:"code"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Permissions []string `json:"permissions"`
	}
	if !httpx.Decode(w, r, &in) || !holdsAll(w, r, in.Permissions) {
		return
	}
	role, err := h.RBAC.CreateRole(r.Context(), access(r).TenantID, in.Code, in.Name, in.Description, in.Permissions)
	if err != nil {
		roleErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, role)
}

// CustomizeRole is POST /roles/customize {code}: the estate's own editable copy of a default role.
func (h *H) CustomizeRole(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	role, err := h.RBAC.CustomizeRole(r.Context(), access(r).TenantID, in.Code)
	if err != nil {
		roleErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, role)
}

// UpdateRole is PUT /roles/{id} {name, description, permissions} on an estate role.
func (h *H) UpdateRole(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Name        *string  `json:"name"`
		Description *string  `json:"description"`
		Permissions []string `json:"permissions"`
	}
	if !httpx.Decode(w, r, &in) || !holdsAll(w, r, in.Permissions) {
		return
	}
	role, err := h.RBAC.UpdateRole(r.Context(), access(r).TenantID, id, in.Name, in.Description, in.Permissions)
	if err != nil {
		roleErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, role)
}

// DeleteRole is DELETE /roles/{id}: a customised role goes back to the default; an estate's own
// role is refused while anyone holds it.
func (h *H) DeleteRole(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	if err := h.RBAC.DeleteRole(r.Context(), access(r).TenantID, id); err != nil {
		roleErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// InviteStaff is POST /users/invite {email, name, phone, roles, property_ids}: adds the person in
// Codevertex accounts over S2S, gives them Maskani roles here and, optionally, properties. The
// response carries a one-time temporary password only when a brand-new account was created.
func (h *H) InviteStaff(w http.ResponseWriter, r *http.Request) {
	var in register.StaffInvite
	if !httpx.Decode(w, r, &in) {
		return
	}
	a := access(r)
	// Someone limited to some properties can only bring staff into those properties; an invite with
	// no properties would give the new person every property.
	if !a.AllProperties && len(in.PropertyIDs) == 0 {
		httpx.Fail(w, httpx.Invalid("choose at least one of your properties for this person"))
		return
	}
	for _, pid := range in.PropertyIDs {
		if !requireProperty(w, r, pid) {
			return
		}
	}
	if !h.canGrantRoles(w, r, in.Roles) {
		return
	}
	// Validate the roles before creating anything in auth-api.
	roles, err := h.RBAC.EffectiveRoles(r.Context(), a.TenantID, in.Roles)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if len(roles) != len(in.Roles) || len(roles) == 0 {
		httpx.Fail(w, httpx.Invalid("choose roles from the list"))
		return
	}
	res, err := h.Register.InviteStaffMember(r.Context(), a.TenantID, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	authID, err := uuid.Parse(res.UserID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	user, err := h.RBAC.EnsureStaffUser(r.Context(), a.TenantID, authID, a.AuthUserID, in.Email, in.Name, in.Roles)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	for _, pid := range in.PropertyIDs {
		if _, err := h.Register.AssignStaff(r.Context(), pid, authID, a.AuthUserID, in.Roles[0], ""); err != nil {
			httpx.Fail(w, err)
			return
		}
	}
	out := map[string]any{"user": user, "auth_user_id": authID, "new_account": res.TempPassword != ""}
	if res.TempPassword != "" {
		out["temp_password"] = res.TempPassword
	}
	httpx.JSON(w, http.StatusCreated, out)
}

// SetUserStatus is PUT /users/{id}/status {status: active|suspended}: a suspended user keeps their
// account elsewhere but has no Maskani access.
func (h *H) SetUserStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	if in.Status != "active" && in.Status != "suspended" {
		httpx.Fail(w, httpx.Invalid("status must be active or suspended"))
		return
	}
	a := access(r)
	u, err := h.RBAC.SetUserStatus(r.Context(), a.TenantID, id, a.AuthUserID, in.Status)
	if err != nil {
		roleErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, u)
}
