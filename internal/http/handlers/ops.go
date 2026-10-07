package handlers

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/gate"
	"github.com/bengobox/maskani-api/internal/modules/notices"
	"github.com/bengobox/maskani-api/internal/modules/utilities"
	"github.com/bengobox/maskani-api/internal/modules/works"
	"github.com/bengobox/maskani-api/internal/shared/page"
)

// --- Utilities ---

// CreateMeter is POST /meters.
func (h *H) CreateMeter(w http.ResponseWriter, r *http.Request) {
	var in utilities.MeterInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	if !requireProperty(w, r, in.PropertyID) {
		return
	}
	m, err := h.Utilities.CreateMeter(r.Context(), in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, m)
}

// ListMeters is GET /meters?property_id=.
func (h *H) ListMeters(w http.ResponseWriter, r *http.Request) {
	pid := httpx.QueryUUID(r, "property_id")
	if pid == nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "property_id is required")
		return
	}
	rows, err := h.Utilities.ListMeters(r.Context(), *pid)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// GetRound is GET /reading-rounds/{period}?property_id=.
func (h *H) GetRound(w http.ResponseWriter, r *http.Request) {
	pid := httpx.QueryUUID(r, "property_id")
	if pid == nil || !requireProperty(w, r, *pid) {
		if pid == nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "property_id is required")
		}
		return
	}
	rd, err := h.Utilities.GetRound(r.Context(), *pid, chiParam(r, "period"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, rd)
}

// RecordReading is POST /meters/{id}/readings.
func (h *H) RecordReading(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	var in utilities.ReadingInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	rd, err := h.Utilities.Record(r.Context(), id, actor(r), in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, rd)
}

// VerifyReading is POST /meter-readings/{id}/verify {action}.
func (h *H) VerifyReading(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Action string `json:"action"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	rd, err := h.Utilities.Verify(r.Context(), id, actor(r), in.Action)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, rd)
}

// EstimateReading is POST /meters/{id}/estimate {period}.
func (h *H) EstimateReading(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Period string `json:"period"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	rd, err := h.Utilities.Estimate(r.Context(), id, actor(r), in.Period)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, rd)
}

// WaterBalance is GET /water-balance?property_id=&period=.
func (h *H) WaterBalance(w http.ResponseWriter, r *http.Request) {
	pid := httpx.QueryUUID(r, "property_id")
	if pid == nil || !requireProperty(w, r, *pid) {
		if pid == nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "property_id is required")
		}
		return
	}
	pts, err := h.Utilities.WaterBalance(r.Context(), *pid, period(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": pts})
}

// --- Works ---

func (h *H) staffActor(r *http.Request) works.Actor {
	return works.Actor{UserID: actor(r), Kind: "staff"}
}

// ListWorkOrders is GET /work-orders.
func (h *H) ListWorkOrders(w http.ResponseWriter, r *http.Request) {
	a := access(r)
	res, err := h.Works.List(r.Context(), works.Filter{PropertyID: httpx.QueryUUID(r, "property_id"),
		Status: r.URL.Query().Get("status"), Priority: r.URL.Query().Get("priority"),
		Overdue: r.URL.Query().Get("overdue") == "true", Scope: a.PropertyIDs, AllProperties: a.AllProperties}, page.Parse(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

// CreateWorkOrder is POST /work-orders.
func (h *H) CreateWorkOrder(w http.ResponseWriter, r *http.Request) {
	var in works.RequestInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	if !requireProperty(w, r, in.PropertyID) {
		return
	}
	wo, err := h.Works.Create(r.Context(), h.staffActor(r), in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	h.Reports.Invalidate(access(r).TenantID)
	httpx.JSON(w, http.StatusCreated, wo)
}

// GetWorkOrder is GET /work-orders/{id}.
func (h *H) GetWorkOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	wo, err := h.Works.Get(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if !requireProperty(w, r, wo.PropertyID) {
		return
	}
	httpx.JSON(w, http.StatusOK, wo)
}

// ActWorkOrder is POST /work-orders/{id}/actions.
func (h *H) ActWorkOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	var in works.ActionInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	wo, err := h.Works.Act(r.Context(), id, h.staffActor(r), in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	h.Reports.Invalidate(access(r).TenantID)
	httpx.JSON(w, http.StatusOK, wo)
}

// ListVendors is GET /vendors.
func (h *H) ListVendors(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Works.ListVendors(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// CreateVendor is POST /vendors.
func (h *H) CreateVendor(w http.ResponseWriter, r *http.Request) {
	var in works.VendorInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	v, err := h.Works.CreateVendor(r.Context(), actor(r), in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, v)
}

// AddVendorDocument is POST /vendors/{id}/documents.
func (h *H) AddVendorDocument(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	var in works.DocumentInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	d, err := h.Works.AddDocument(r.Context(), id, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, d)
}

// AddPersonnel is POST /vendors/{id}/personnel.
func (h *H) AddPersonnel(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		FullName    string   `json:"full_name"`
		Role        string   `json:"role"`
		Phone       string   `json:"phone"`
		BadgeNumber string   `json:"badge_number"`
		PropertyIDs []string `json:"property_ids"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	p, err := h.Works.AddPersonnel(r.Context(), id, in.FullName, in.Role, in.Phone, in.BadgeNumber, in.PropertyIDs)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, p)
}

// --- Gate (staff side) ---

// RegisterDevice is POST /gate/devices.
func (h *H) RegisterDevice(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PropertyID uuid.UUID `json:"property_id"`
		Name       string    `json:"name"`
		GateName   string    `json:"gate_name"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	if !requireProperty(w, r, in.PropertyID) {
		return
	}
	d, key, err := h.Gate.RegisterDevice(r.Context(), in.PropertyID, actor(r), in.Name, in.GateName)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"device": d, "device_key": key, "tenant_slug": access(r).TenantSlug})
}

// StaffCreatePass is POST /visitor-passes.
func (h *H) StaffCreatePass(w http.ResponseWriter, r *http.Request) {
	var in gate.PassInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	if !requireProperty(w, r, in.PropertyID) {
		return
	}
	p, err := h.Gate.CreatePass(r.Context(), "staff", actor(r), nil, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, p)
}

// ListPasses is GET /visitor-passes?property_id=&active=.
func (h *H) ListPasses(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Gate.ListPasses(r.Context(), httpx.QueryUUID(r, "property_id"), nil, r.URL.Query().Get("active") == "true", intQuery(r, "limit", 200))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// ListGateEvents is GET /gate/events?property_id=.
func (h *H) ListGateEvents(w http.ResponseWriter, r *http.Request) {
	pid := httpx.QueryUUID(r, "property_id")
	if pid == nil || !requireProperty(w, r, *pid) {
		if pid == nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "property_id is required")
		}
		return
	}
	res, err := h.Gate.ListEvents(r.Context(), *pid, page.Parse(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

// ReportIncident is POST /incidents.
func (h *H) ReportIncident(w http.ResponseWriter, r *http.Request) {
	var in gate.IncidentInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	inc, err := h.Gate.ReportIncident(r.Context(), "staff", actor(r), in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, inc)
}

// ListIncidents is GET /incidents.
func (h *H) ListIncidents(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Gate.ListIncidents(r.Context(), httpx.QueryUUID(r, "property_id"), r.URL.Query().Get("open") == "true", intQuery(r, "limit", 200))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// --- Notices ---

// CreateNotice is POST /notices.
func (h *H) CreateNotice(w http.ResponseWriter, r *http.Request) {
	var in notices.Input
	if !httpx.Decode(w, r, &in) {
		return
	}
	n, err := h.Notices.Create(r.Context(), actor(r), access(r).TenantSlug, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, n)
}

// SendNotice is POST /notices/{id}/send.
func (h *H) SendNotice(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	n, err := h.Notices.Send(r.Context(), id, access(r).TenantSlug)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, n)
}

// ListNotices is GET /notices.
func (h *H) ListNotices(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Notices.List(r.Context(), httpx.QueryUUID(r, "property_id"), intQuery(r, "limit", 50))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// NoticeDeliveries is GET /notices/{id}/deliveries.
func (h *H) NoticeDeliveries(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	rows, err := h.Notices.Deliveries(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// --- Reports and enquiries ---

// Dashboard is GET /reports/dashboard?property_id=&period=.
func (h *H) Dashboard(w http.ResponseWriter, r *http.Request) {
	pid := httpx.QueryUUID(r, "property_id")
	if pid != nil && !requireProperty(w, r, *pid) {
		return
	}
	d, err := h.Reports.Dashboard(r.Context(), pid, r.URL.Query().Get("period"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, d)
}

// Arrears is GET /reports/arrears.
func (h *H) Arrears(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Reports.Arrears(r.Context(), httpx.QueryUUID(r, "property_id"), intQuery(r, "limit", 200))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// SalesPosition is GET /reports/sales-position?property_id=.
func (h *H) SalesPosition(w http.ResponseWriter, r *http.Request) {
	pid := httpx.QueryUUID(r, "property_id")
	if pid == nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "property_id is required")
		return
	}
	v, err := h.Reports.Sales(r.Context(), *pid)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, v)
}

// ListEnquiries is GET /enquiries.
func (h *H) ListEnquiries(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Market.ListEnquiries(r.Context(), r.URL.Query().Get("status"), intQuery(r, "limit", 200))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// UpdateEnquiry is PATCH /enquiries/{id}.
func (h *H) UpdateEnquiry(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Status     string     `json:"status"`
		AssignedTo *uuid.UUID `json:"assigned_to"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	if err := h.Market.UpdateEnquiry(r.Context(), id, in.Status, in.AssignedTo); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
