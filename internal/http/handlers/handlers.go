// Package handlers holds the HTTP handlers. Handlers stay thin: parse, authorise against the
// request's Access, call a service, respond.
package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	mw "github.com/bengobox/maskani-api/internal/http/middleware"
	"github.com/bengobox/maskani-api/internal/modules/accounts"
	"github.com/bengobox/maskani-api/internal/modules/billing"
	"github.com/bengobox/maskani-api/internal/modules/collections"
	"github.com/bengobox/maskani-api/internal/modules/docs"
	"github.com/bengobox/maskani-api/internal/modules/gate"
	"github.com/bengobox/maskani-api/internal/modules/imports"
	"github.com/bengobox/maskani-api/internal/modules/market"
	"github.com/bengobox/maskani-api/internal/modules/notices"
	"github.com/bengobox/maskani-api/internal/modules/portal"
	"github.com/bengobox/maskani-api/internal/modules/rbac"
	"github.com/bengobox/maskani-api/internal/modules/register"
	"github.com/bengobox/maskani-api/internal/modules/reports"
	"github.com/bengobox/maskani-api/internal/modules/sales"
	"github.com/bengobox/maskani-api/internal/modules/sequence"
	"github.com/bengobox/maskani-api/internal/modules/settings"
	"github.com/bengobox/maskani-api/internal/modules/utilities"
	"github.com/bengobox/maskani-api/internal/modules/works"
	"github.com/bengobox/maskani-api/internal/platform/realtime"
)

// H bundles the services handlers call.
type H struct {
	RBAC        *rbac.Service
	Settings    *settings.Service
	Register    *register.Service
	Accounts    *accounts.Service
	Billing     *billing.Service
	Collections *collections.Service
	Utilities   *utilities.Service
	Sales       *sales.Service
	Works       *works.Service
	Gate        *gate.Service
	Notices     *notices.Service
	Reports     *reports.Service
	Portal      *portal.Service
	Market      *market.Service
	Imports     *imports.Service
	Docs        *docs.Service
	Sequences   *sequence.Allocator
	PortalURL   string
	Media       *Media
	// RT is the realtime hub behind GET /stream (nil disables live updates).
	RT *realtime.Hub
}

func access(r *http.Request) *mw.Access { return mw.FromContext(r.Context()) }

// actor is the caller's auth user id (uuid.Nil for S2S).
func actor(r *http.Request) uuid.UUID {
	if a := access(r); a != nil {
		return a.AuthUserID
	}
	return uuid.Nil
}

func intQuery(r *http.Request, name string, def int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return h
	}
	return ""
}

func period(r *http.Request) string {
	if p := r.URL.Query().Get("period"); p != "" {
		return p
	}
	return time.Now().Format("2006-01")
}

// requireProperty enforces property scope on a property id from the URL or query.
func requireProperty(w http.ResponseWriter, r *http.Request, id uuid.UUID) bool {
	if !access(r).CanSeeProperty(id) {
		httpx.Error(w, http.StatusForbidden, "forbidden", "you are not assigned to this property")
		return false
	}
	return true
}

// scopeOf enforces property scope on a record addressed by id: it looks up the record's property
// and rejects callers not assigned to it. Every write route keyed by a record id goes through here.
func (h *H) scopeOf(w http.ResponseWriter, r *http.Request, kind register.Record, id uuid.UUID) bool {
	a := access(r)
	if a != nil && a.AllProperties {
		return true
	}
	pid, err := h.Register.PropertyOf(r.Context(), kind, id)
	if err != nil {
		httpx.Fail(w, err)
		return false
	}
	return requireProperty(w, r, pid)
}

// Me is GET /auth/me: identity, roles, permissions, modules, assigned properties and party links.
func (h *H) Me(w http.ResponseWriter, r *http.Request) {
	a := access(r)
	roles, perms := a.Roles, a.Perms
	if roles == nil {
		roles = []string{}
	}
	if perms == nil {
		perms = []string{}
	}
	out := map[string]any{
		"id": a.AuthUserID, "email": a.Email, "tenant_id": a.TenantID, "tenant_slug": a.TenantSlug,
		"roles": roles, "permissions": perms, "is_platform_owner": a.Claims != nil && a.Claims.IsPlatformOwner,
		"all_properties": a.AllProperties, "property_ids": a.PropertyIDs, "party_ids": a.PartyIDs,
		"is_staff": a.IsStaff(), "is_portal_user": len(a.PartyIDs) > 0, "bypass": a.Bypass,
		"modules": h.Settings.ModuleList(r.Context(), a.TenantID),
	}
	if a.IsStaff() || a.Bypass {
		// Properties whose use case or switches narrow the tenant's modules; any other property
		// uses "modules" as is.
		out["property_modules"] = h.Settings.PropertyModuleMap(r.Context(), a.TenantID)
	}
	if a.LocalUser != nil {
		out["user"] = a.LocalUser
	}
	if len(a.PartyIDs) > 0 && h.Portal != nil {
		// The portal compares this with the estate's terms version instead of trusting the device.
		out["terms_accepted_version"] = h.Portal.AcceptedTerms(r.Context(), a.PartyIDs)
	}
	if s, err := h.Settings.Get(r.Context(), a.TenantID); err == nil {
		out["settings"] = s
	}
	httpx.JSON(w, http.StatusOK, out)
}

// GetSettings is GET /settings.
func (h *H) GetSettings(w http.ResponseWriter, r *http.Request) {
	s, err := h.Settings.Get(r.Context(), access(r).TenantID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, s)
}

// UpdateSettings is PUT /settings.
func (h *H) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var in settings.UpdateInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	s, err := h.Settings.Update(r.Context(), access(r).TenantID, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, s)
}

// GetModules is GET /settings/modules.
func (h *H) GetModules(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]any{
		"enabled": h.Settings.ModuleList(r.Context(), access(r).TenantID), "presets": settings.Presets,
		"released": settings.ReleasedModules, "dependencies": settings.ModuleDependencies,
	})
}

// SetModules is PUT /settings/modules with {"modules":[...]} or {"preset":"..."}.
func (h *H) SetModules(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Modules []string `json:"modules"`
		Preset  string   `json:"preset"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	a := access(r)
	var err error
	if in.Preset != "" {
		err = h.Settings.ApplyPreset(r.Context(), a.TenantID, in.Preset, a.AuthUserID)
	} else {
		err = h.Settings.SetModules(r.Context(), a.TenantID, in.Modules, a.AuthUserID)
	}
	if err != nil {
		httpx.Fail(w, httpx.Invalid(err.Error()))
		return
	}
	h.GetModules(w, r)
}

// Catalogue is GET /catalogues/{kind}: staff, and portal users for the lists their forms use.
func (h *H) Catalogue(w http.ResponseWriter, r *http.Request) {
	if a := access(r); a == nil || (!a.IsStaff() && len(a.PartyIDs) == 0) {
		httpx.Error(w, http.StatusForbidden, "forbidden", "sign in to this estate to see its lists")
		return
	}
	rows, err := h.Settings.Catalogue(r.Context(), access(r).TenantID, chiParam(r, "kind"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// UpsertCatalogue is PUT /catalogues/{kind}/{code}: settings managers, or whoever manages what the
// list describes (rbac.CatalogueManagePerms).
func (h *H) UpsertCatalogue(w http.ResponseWriter, r *http.Request) {
	a := access(r)
	if !a.Has(append([]string{rbac.PermSettingsManage}, rbac.CatalogueManagePerms[chiParam(r, "kind")]...)...) {
		httpx.Error(w, http.StatusForbidden, "forbidden", "you cannot change this list")
		return
	}
	var in struct {
		Name   string         `json:"name"`
		Active *bool          `json:"active"`
		Attrs  map[string]any `json:"attrs"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	active := true
	if in.Active != nil {
		active = *in.Active
	}
	if err := h.Settings.UpsertCatalogEntry(r.Context(), access(r).TenantID, chiParam(r, "kind"), chiParam(r, "code"), in.Name, active, in.Attrs); err != nil {
		httpx.Fail(w, err)
		return
	}
	h.Catalogue(w, r)
}

// ListUsers is GET /users?kind=.
func (h *H) ListUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := h.RBAC.ListUsers(r.Context(), access(r).TenantID, r.URL.Query().Get("kind"), r.URL.Query().Get("q"), intQuery(r, "limit", 200))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// SetUserRoles is PUT /users/{id}/roles.
func (h *H) SetUserRoles(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Roles []string `json:"roles"`
	}
	if !httpx.Decode(w, r, &in) || !h.canGrantRoles(w, r, in.Roles) {
		return
	}
	a := access(r)
	if err := h.RBAC.SetUserRoles(r.Context(), a.TenantID, id, a.AuthUserID, in.Roles); err != nil {
		httpx.Fail(w, httpx.Invalid(err.Error()))
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
