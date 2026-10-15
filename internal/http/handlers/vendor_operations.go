package handlers

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/works"
)

// inScope loads an id and checks the caller may act on the property it belongs to.
func inScope(w http.ResponseWriter, r *http.Request, propertyOf func(*http.Request, uuid.UUID) (uuid.UUID, error)) (uuid.UUID, bool) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return id, false
	}
	pid, err := propertyOf(r, id)
	if err != nil {
		httpx.Fail(w, err)
		return id, false
	}
	return id, requireProperty(w, r, pid)
}

// ContractVisitPlans is GET /vendor-contracts/{id}/schedules: the contract's visit plans.
func (h *H) ContractVisitPlans(w http.ResponseWriter, r *http.Request) {
	id, ok := h.contractInScope(w, r)
	if !ok {
		return
	}
	rows, err := h.Works.ContractSchedules(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// CreateVisitPlan is POST /vendor-contracts/{id}/schedules.
func (h *H) CreateVisitPlan(w http.ResponseWriter, r *http.Request) {
	id, ok := h.contractInScope(w, r)
	if !ok {
		return
	}
	var in works.ScheduleInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	sc, err := h.Works.CreateSchedule(r.Context(), id, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, sc)
}

// UpdateVisitPlan is PUT /service-schedules/{id}.
func (h *H) UpdateVisitPlan(w http.ResponseWriter, r *http.Request) {
	id, ok := inScope(w, r, func(r *http.Request, id uuid.UUID) (uuid.UUID, error) { return h.Works.ScheduleProperty(r.Context(), id) })
	if !ok {
		return
	}
	var in works.ScheduleInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	sc, err := h.Works.UpdateSchedule(r.Context(), id, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, sc)
}

// ListServiceVisits is GET /service-visits?property_id=&from=&to=&contract_id=&status=.
func (h *H) ListServiceVisits(w http.ResponseWriter, r *http.Request) {
	pid := httpx.QueryUUID(r, "property_id")
	if pid == nil {
		httpx.Fail(w, httpx.Invalid("property_id is required"))
		return
	}
	if !requireProperty(w, r, *pid) {
		return
	}
	q := r.URL.Query()
	from, err1 := time.Parse("2006-01-02", q.Get("from"))
	to, err2 := time.Parse("2006-01-02", q.Get("to"))
	if err1 != nil || err2 != nil {
		httpx.Fail(w, httpx.Invalid("from and to are dates (YYYY-MM-DD)"))
		return
	}
	rows, err := h.Works.Visits(r.Context(), works.VisitFilter{PropertyID: *pid, ContractID: httpx.QueryUUID(r, "contract_id"),
		Status: q.Get("status"), From: from, To: to})
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// RecordServiceVisit is POST /service-visits/{id}/record {status, photos, checklist_result}.
func (h *H) RecordServiceVisit(w http.ResponseWriter, r *http.Request) {
	id, ok := inScope(w, r, func(r *http.Request, id uuid.UUID) (uuid.UUID, error) { return h.Works.VisitProperty(r.Context(), id) })
	if !ok {
		return
	}
	var in works.VisitInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	v, err := h.Works.RecordVisit(r.Context(), id, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, v)
}

// ListMaintenancePlans is GET /maintenance-schedules?property_id=.
func (h *H) ListMaintenancePlans(w http.ResponseWriter, r *http.Request) {
	f, ok := scopeFilter(w, r)
	if !ok {
		return
	}
	ids, all := f.Scope, f.AllProperties
	if f.PropertyID != nil {
		ids, all = []uuid.UUID{*f.PropertyID}, false
	}
	rows, err := h.Works.MaintenancePlans(r.Context(), ids, all)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// CreateMaintenancePlan is POST /maintenance-schedules.
func (h *H) CreateMaintenancePlan(w http.ResponseWriter, r *http.Request) {
	var in works.MaintenanceInput
	if !httpx.Decode(w, r, &in) || !requireProperty(w, r, in.PropertyID) {
		return
	}
	m, err := h.Works.CreateMaintenance(r.Context(), in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, m)
}

// UpdateMaintenancePlan is PUT /maintenance-schedules/{id}.
func (h *H) UpdateMaintenancePlan(w http.ResponseWriter, r *http.Request) {
	id, ok := inScope(w, r, func(r *http.Request, id uuid.UUID) (uuid.UUID, error) { return h.Works.MaintenanceProperty(r.Context(), id) })
	if !ok {
		return
	}
	var in works.MaintenanceInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	m, err := h.Works.UpdateMaintenance(r.Context(), id, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, m)
}
