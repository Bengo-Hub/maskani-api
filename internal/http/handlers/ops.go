package handlers

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/approvals"
	"github.com/bengobox/maskani-api/internal/modules/gate"
	"github.com/bengobox/maskani-api/internal/modules/notices"
	"github.com/bengobox/maskani-api/internal/modules/rbac"
	"github.com/bengobox/maskani-api/internal/modules/register"
	"github.com/bengobox/maskani-api/internal/modules/reports"
	"github.com/bengobox/maskani-api/internal/modules/utilities"
	"github.com/bengobox/maskani-api/internal/modules/works"
	"github.com/bengobox/maskani-api/internal/shared/page"
	"github.com/bengobox/maskani-api/internal/shared/secure"
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
	if !requireProperty(w, r, *pid) {
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
	if !h.scopeOf(w, r, register.RecordMeter, id) {
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
	if !h.scopeOf(w, r, register.RecordReading, id) {
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
	if !h.scopeOf(w, r, register.RecordMeter, id) {
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
	pts, err := h.Reports.WaterBalance(r.Context(), *pid, period(r))
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
	if !h.scopeOf(w, r, register.RecordWorkOrder, id) {
		return
	}
	var wo *ent.WorkOrder
	var err error
	switch in.Action {
	case "approve_quote", "reject_quote":
		d := approvals.Approve
		if in.Action == "reject_quote" {
			d = approvals.Reject
		}
		wo, err = h.Works.DecideQuote(r.Context(), id, approver(r), d, in.Note)
	default:
		wo, err = h.Works.Act(r.Context(), id, h.staffActor(r), in)
	}
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	h.Reports.Invalidate(access(r).TenantID)
	httpx.JSON(w, http.StatusOK, wo)
}

// ListVendors is GET /vendors?status= (keyset page).
func (h *H) ListVendors(w http.ResponseWriter, r *http.Request) {
	res, err := h.Works.ListVendors(r.Context(), r.URL.Query().Get("status"), page.Parse(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

// GetVendor is GET /vendors/{id}: vendor with documents and personnel (has_pin, never the hash).
func (h *H) GetVendor(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	a := access(r)
	v, err := h.Works.GetVendor(r.Context(), id, a.PropertyIDs, a.AllProperties)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, v)
}

// SetPersonnelPIN is PUT /vendors/{id}/personnel/{pid}/pin {pin}: sets a guard's gate PIN.
func (h *H) SetPersonnelPIN(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	pid, ok := httpx.UUIDParam(w, r, "pid")
	if !ok {
		return
	}
	var in struct {
		PIN string `json:"pin"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	p, err := h.Works.SetGuardPIN(r.Context(), id, pid, in.PIN)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, p)
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
	httpx.JSON(w, http.StatusCreated, works.PersonnelView{VendorPersonnel: p, HasPIN: p.PinHash != ""})
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

// scopeFilter reads ?property_id and checks it against the caller's properties.
func scopeFilter(w http.ResponseWriter, r *http.Request) (gate.ScopeFilter, bool) {
	a := access(r)
	f := gate.ScopeFilter{PropertyID: httpx.QueryUUID(r, "property_id"), Scope: a.PropertyIDs, AllProperties: a.AllProperties}
	if f.PropertyID != nil && !requireProperty(w, r, *f.PropertyID) {
		return f, false
	}
	return f, true
}

// ListPasses is GET /visitor-passes?property_id=&active= (keyset page).
func (h *H) ListPasses(w http.ResponseWriter, r *http.Request) {
	f, ok := scopeFilter(w, r)
	if !ok {
		return
	}
	res, err := h.Gate.PagePassViews(r.Context(), f, r.URL.Query().Get("active") == "true", page.Parse(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

// GetPass is GET /visitor-passes/{id}: one pass with its unit and block (property scoped).
func (h *H) GetPass(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	p, err := h.Gate.Pass(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if !requireProperty(w, r, p.PropertyID) {
		return
	}
	httpx.JSON(w, http.StatusOK, p)
}

// StaffCancelPass is POST /visitor-passes/{id}/cancel.
func (h *H) StaffCancelPass(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	p, err := h.Gate.Pass(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if !requireProperty(w, r, p.PropertyID) {
		return
	}
	if err := h.Gate.CancelActivePass(r.Context(), id); err != nil {
		httpx.Fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListDevices is GET /gate/devices?property_id=: the property's tablets with last seen and online.
func (h *H) ListDevices(w http.ResponseWriter, r *http.Request) {
	pid := httpx.QueryUUID(r, "property_id")
	if pid == nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "property_id is required")
		return
	}
	if !requireProperty(w, r, *pid) {
		return
	}
	rows, err := h.Gate.Devices(r.Context(), *pid)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// RevokeDevice is POST /gate/devices/{id}/revoke: the tablet's key stops working.
func (h *H) RevokeDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	pid, err := h.Gate.DevicePropertyID(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if !requireProperty(w, r, pid) {
		return
	}
	if err := h.Gate.RevokeDevice(r.Context(), id); err != nil {
		httpx.Fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	kind := r.URL.Query().Get("kind")
	if kind != "" && !gate.ValidEventKind(kind) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "unknown event kind")
		return
	}
	res, err := h.Gate.ListEvents(r.Context(), *pid, kind, page.Parse(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

// GateInside is GET /gate/inside?property_id=: who is inside now (staff view of the gate).
func (h *H) GateInside(w http.ResponseWriter, r *http.Request) {
	pid := httpx.QueryUUID(r, "property_id")
	if pid == nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "property_id is required")
		return
	}
	if !requireProperty(w, r, *pid) {
		return
	}
	rows, err := h.Gate.Inside(r.Context(), *pid)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// ReportIncident is POST /incidents.
func (h *H) ReportIncident(w http.ResponseWriter, r *http.Request) {
	var in gate.IncidentInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	if !requireProperty(w, r, in.PropertyID) {
		return
	}
	inc, err := h.Gate.ReportIncident(r.Context(), "staff", actor(r), in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, inc)
}

// GetIncident is GET /incidents/{id}, limited to properties the caller can see.
func (h *H) GetIncident(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	inc, err := h.Gate.Incident(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if !requireProperty(w, r, inc.PropertyID) {
		return
	}
	httpx.JSON(w, http.StatusOK, inc)
}

// ListIncidents is GET /incidents?property_id=&open= (keyset page).
func (h *H) ListIncidents(w http.ResponseWriter, r *http.Request) {
	f, ok := scopeFilter(w, r)
	if !ok {
		return
	}
	res, err := h.Gate.PageIncidents(r.Context(), f, r.URL.Query().Get("open") == "true", page.Parse(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

// --- Notices ---

// CreateNotice is POST /notices.
func (h *H) CreateNotice(w http.ResponseWriter, r *http.Request) {
	var in notices.Input
	if !httpx.Decode(w, r, &in) {
		return
	}
	// An estate-wide notice (no property) needs access to every property.
	pid := uuid.Nil
	if in.PropertyID != nil {
		pid = *in.PropertyID
	}
	if !requireProperty(w, r, pid) {
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
	if !ok || !h.scopeOf(w, r, register.RecordNotice, id) {
		return
	}
	n, err := h.Notices.Send(r.Context(), id, access(r).TenantSlug)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, n)
}

// ListNotices is GET /notices?property_id=&status= (keyset page).
func (h *H) ListNotices(w http.ResponseWriter, r *http.Request) {
	f, ok := scopeFilter(w, r)
	if !ok {
		return
	}
	res, err := h.Notices.List(r.Context(), f.PropertyID, f.Scope, f.AllProperties, r.URL.Query().Get("status"), page.Parse(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

// NoticeDeliveries is GET /notices/{id}/deliveries.
func (h *H) NoticeDeliveries(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok || !h.scopeOf(w, r, register.RecordNotice, id) {
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

// Dashboard is GET /reports/dashboard?property_id=&from=&to=&block_id=&fund= (period= still means
// one month).
func (h *H) Dashboard(w http.ResponseWriter, r *http.Request) {
	f, ok := scopeFilter(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	df := reports.DashboardFilter{From: q.Get("from"), To: q.Get("to"), BlockID: httpx.QueryUUID(r, "block_id")}
	if p := q.Get("period"); p != "" && df.From == "" && df.To == "" {
		df.From, df.To = p, p
	}
	blockProp, fundID, err := h.Reports.FilterIDs(r.Context(), df.BlockID, q.Get("fund"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	// A block narrows to its property, which the caller must be able to see.
	if blockProp != nil {
		if !requireProperty(w, r, *blockProp) {
			return
		}
		f.PropertyID = blockProp
	}
	df.FundID = fundID
	d, err := h.Reports.Dashboard(r.Context(), reports.Scope{PropertyID: f.PropertyID, IDs: f.Scope, All: f.AllProperties}, df)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, d)
}

// Insights is GET /reports/insights?property_id=&period=: trends, comparisons and forecasts for
// the staff dashboard.
func (h *H) Insights(w http.ResponseWriter, r *http.Request) {
	f, ok := scopeFilter(w, r)
	if !ok {
		return
	}
	v, err := h.Reports.Insights(r.Context(), reports.Scope{PropertyID: f.PropertyID, IDs: f.Scope, All: f.AllProperties},
		r.URL.Query().Get("period"))
	if err != nil {
		httpx.Fail(w, httpx.Invalid(err.Error()))
		return
	}
	httpx.JSON(w, http.StatusOK, v)
}

// RoleSummary is GET /reports/role-summary?property_id=: what each kind of staff acts on today.
// Open to all staff; the screen shows only the panels the caller's role uses.
func (h *H) RoleSummary(w http.ResponseWriter, r *http.Request) {
	if !access(r).IsStaff() {
		httpx.Error(w, http.StatusForbidden, "forbidden", "staff only")
		return
	}
	f, ok := scopeFilter(w, r)
	if !ok {
		return
	}
	v, err := h.Reports.Roles(r.Context(), reports.Scope{PropertyID: f.PropertyID, IDs: f.Scope, All: f.AllProperties})
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, v)
}

// Arrears is GET /reports/arrears?property_id= (keyset page, largest balance first).
func (h *H) Arrears(w http.ResponseWriter, r *http.Request) {
	f, ok := scopeFilter(w, r)
	if !ok {
		return
	}
	res, err := h.Reports.Arrears(r.Context(), f.PropertyID, f.Scope, f.AllProperties, arrearsFilter(r), page.ParseDecimal(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if maskArrearsPhones(r) {
		for i := range res.Data {
			res.Data[i].Phone = secure.MaskPhone(res.Data[i].Phone)
		}
	}
	httpx.JSON(w, http.StatusOK, res)
}

// arrearsFilter reads ?q (account prefix or name) and ?min (smallest balance).
func arrearsFilter(r *http.Request) reports.ArrearsFilter {
	af := reports.ArrearsFilter{Q: r.URL.Query().Get("q")}
	if v := r.URL.Query().Get("min"); v != "" {
		if d, err := decimal.NewFromString(v); err == nil {
			af.Min = d
		}
	}
	return af
}

// maskArrearsPhones: the phone is for whoever follows up arrears; report viewers see it masked.
func maskArrearsPhones(r *http.Request) bool {
	return !access(r).Has(rbac.PermBillingCollect, rbac.PermBillingManage)
}

// SalesPosition is GET /reports/sales-position?property_id=.
func (h *H) SalesPosition(w http.ResponseWriter, r *http.Request) {
	pid := httpx.QueryUUID(r, "property_id")
	if pid == nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "property_id is required")
		return
	}
	if !requireProperty(w, r, *pid) {
		return
	}
	v, err := h.Reports.Sales(r.Context(), *pid)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, v)
}

// ListEnquiries is GET /enquiries?property_id=&status= (keyset page).
func (h *H) ListEnquiries(w http.ResponseWriter, r *http.Request) {
	f, ok := scopeFilter(w, r)
	if !ok {
		return
	}
	res, err := h.Market.ListEnquiries(r.Context(), f.PropertyID, f.Scope, f.AllProperties, r.URL.Query().Get("status"), page.Parse(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
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
	if !h.scopeOf(w, r, register.RecordEnquiry, id) {
		return
	}
	if err := h.Market.UpdateEnquiry(r.Context(), id, in.Status, in.AssignedTo); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
