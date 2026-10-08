package handlers

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/register"
	"github.com/bengobox/maskani-api/internal/shared/page"
)

func chiParam(r *http.Request, name string) string { return chi.URLParam(r, name) }

// ListProperties is GET /properties.
func (h *H) ListProperties(w http.ResponseWriter, r *http.Request) {
	a := access(r)
	rows, err := h.Register.ListProperties(r.Context(), a.PropertyIDs, a.AllProperties, r.URL.Query().Get("status"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// CreateProperty is POST /properties.
func (h *H) CreateProperty(w http.ResponseWriter, r *http.Request) {
	var in register.PropertyInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	a := access(r)
	p, err := h.Register.CreateProperty(r.Context(), a.TenantSlug, bearer(r), a.AuthUserID, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, p)
}

// GetProperty is GET /properties/{id}.
func (h *H) GetProperty(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok || !requireProperty(w, r, id) {
		return
	}
	p, err := h.Register.GetProperty(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, p)
}

// UpdateProperty is PATCH /properties/{id}.
func (h *H) UpdateProperty(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok || !requireProperty(w, r, id) {
		return
	}
	var in register.PropertyInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	p, err := h.Register.UpdateProperty(r.Context(), id, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, p)
}

// CreateBlock is POST /properties/{id}/blocks.
func (h *H) CreateBlock(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok || !requireProperty(w, r, id) {
		return
	}
	var in register.BlockInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	b, err := h.Register.CreateBlock(r.Context(), id, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, b)
}

// ListUnits is GET /units.
func (h *H) ListUnits(w http.ResponseWriter, r *http.Request) {
	a := access(r)
	f := register.UnitFilter{PropertyID: httpx.QueryUUID(r, "property_id"), BlockID: httpx.QueryUUID(r, "block_id"),
		SaleStatus: r.URL.Query().Get("sale_status"), OccupancyStatus: r.URL.Query().Get("occupancy_status"),
		Q: r.URL.Query().Get("q"), Scope: a.PropertyIDs, AllProperties: a.AllProperties}
	if f.PropertyID != nil && !requireProperty(w, r, *f.PropertyID) {
		return
	}
	res, err := h.Register.ListUnits(r.Context(), f, page.Parse(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

// CreateUnit is POST /units.
func (h *H) CreateUnit(w http.ResponseWriter, r *http.Request) {
	var in register.UnitInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	if in.PropertyID != nil && !requireProperty(w, r, *in.PropertyID) {
		return
	}
	u, err := h.Register.CreateUnit(r.Context(), actor(r), in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, u)
}

// GetUnit is GET /units/{id}.
func (h *H) GetUnit(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	u, err := h.Register.GetUnit(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if !requireProperty(w, r, u.PropertyID) {
		return
	}
	httpx.JSON(w, http.StatusOK, u)
}

// UpdateUnit is PATCH /units/{id}.
func (h *H) UpdateUnit(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	var in register.UnitInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	cur, err := h.Register.GetUnit(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if !requireProperty(w, r, cur.PropertyID) {
		return
	}
	u, err := h.Register.UpdateUnit(r.Context(), id, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, u)
}

// LinkParty is POST /units/{id}/parties.
func (h *H) LinkParty(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	var in register.LinkInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	if !h.unitScope(w, r, id) {
		return
	}
	l, err := h.Register.LinkParty(r.Context(), id, actor(r), in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, l)
}

// EndLink is POST /unit-parties/{id}/end.
func (h *H) EndLink(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		EndDate *time.Time `json:"end_date"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	end := time.Now()
	if in.EndDate != nil {
		end = *in.EndDate
	}
	l, err := h.Register.EndLink(r.Context(), id, end)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, l)
}

// ListParties is GET /parties.
func (h *H) ListParties(w http.ResponseWriter, r *http.Request) {
	res, err := h.Register.ListParties(r.Context(), r.URL.Query().Get("q"), page.Parse(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

// GetParty is GET /parties/{id}: masked identity numbers and unit links with unit codes.
func (h *H) GetParty(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	a := access(r)
	p, err := h.Register.GetParty(r.Context(), id, a.PropertyIDs, a.AllProperties)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, p)
}

// unitScope checks the caller may act on a unit's property.
func (h *H) unitScope(w http.ResponseWriter, r *http.Request, unitID uuid.UUID) bool {
	pid, err := h.Register.UnitPropertyID(r.Context(), unitID)
	if err != nil {
		httpx.Fail(w, err)
		return false
	}
	return requireProperty(w, r, pid)
}

// accountScope checks the caller may act on a unit account's property.
func (h *H) accountScope(w http.ResponseWriter, r *http.Request, accountID uuid.UUID) bool {
	pid, err := h.Register.AccountPropertyID(r.Context(), accountID)
	if err != nil {
		httpx.Fail(w, err)
		return false
	}
	return requireProperty(w, r, pid)
}

// CreateParty is POST /parties.
func (h *H) CreateParty(w http.ResponseWriter, r *http.Request) {
	var in register.PartyInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	p, err := h.Register.CreateParty(r.Context(), actor(r), in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, h.Register.View(p))
}

// UpdateParty is PATCH /parties/{id}.
func (h *H) UpdateParty(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	var in register.PartyInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	p, err := h.Register.UpdateParty(r.Context(), id, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, h.Register.View(p))
}

// InviteParty is POST /parties/{id}/invite.
func (h *H) InviteParty(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	a := access(r)
	p, err := h.Register.Invite(r.Context(), id, a.TenantSlug, h.PortalURL+"/"+a.TenantSlug+"/portal")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, h.Register.View(p))
}

// ListStaff is GET /properties/{id}/staff.
func (h *H) ListStaff(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok || !requireProperty(w, r, id) {
		return
	}
	rows, err := h.Register.ListStaff(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// AssignStaff is POST /properties/{id}/staff.
func (h *H) AssignStaff(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok || !requireProperty(w, r, id) {
		return
	}
	var in struct {
		AuthUserID    uuid.UUID `json:"auth_user_id"`
		PropertyRole  string    `json:"property_role"`
		ERPEmployeeID string    `json:"erp_employee_id"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	if in.PropertyRole == "" {
		in.PropertyRole = "other"
	}
	row, err := h.Register.AssignStaff(r.Context(), id, in.AuthUserID, actor(r), in.PropertyRole, in.ERPEmployeeID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, row)
}

// RemoveStaff is DELETE /staff-assignments/{id}.
func (h *H) RemoveStaff(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	if err := h.Register.RemoveStaff(r.Context(), id); err != nil {
		httpx.Fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AddVehicle is POST /units/{id}/vehicles.
func (h *H) AddVehicle(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		PartyID *uuid.UUID `json:"party_id"`
		Plate   string     `json:"plate"`
		Make    string     `json:"make"`
		Model   string     `json:"model"`
		Colour  string     `json:"colour"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	if !h.unitScope(w, r, id) {
		return
	}
	v, err := h.Register.AddVehicle(r.Context(), id, in.PartyID, in.Plate, in.Make, in.Model, in.Colour)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, v)
}
