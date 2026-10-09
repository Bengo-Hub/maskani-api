package handlers

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/billing"
)

// BillingSchedule is GET /billing-schedule?property_id=: the property's schedule and where its
// next run stands (stage, missing readings, run).
func (h *H) BillingSchedule(w http.ResponseWriter, r *http.Request) {
	pid := httpx.QueryUUID(r, "property_id")
	if pid == nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "property_id is required")
		return
	}
	if !requireProperty(w, r, *pid) {
		return
	}
	v, err := h.Billing.ScheduleStatus(r.Context(), *pid)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, v)
}

// SaveBillingSchedule is PUT /billing-schedule {property_id, enabled, fund, mode, missing_readings,
// remind_days_before}.
func (h *H) SaveBillingSchedule(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PropertyID uuid.UUID `json:"property_id"`
		billing.ScheduleInput
	}
	if !httpx.Decode(w, r, &in) || !requireProperty(w, r, in.PropertyID) {
		return
	}
	v, err := h.Billing.SaveSchedule(r.Context(), in.PropertyID, in.ScheduleInput)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, v)
}

// ApproveScheduledRun is POST /billing-schedule/approve {property_id, period}: run the period now
// without the readings still missing.
func (h *H) ApproveScheduledRun(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PropertyID uuid.UUID `json:"property_id"`
		Period     string    `json:"period"`
	}
	if !httpx.Decode(w, r, &in) || !requireProperty(w, r, in.PropertyID) {
		return
	}
	run, err := h.Billing.ApproveScheduledRun(r.Context(), actor(r), in.PropertyID, in.Period)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	h.Reports.Invalidate(access(r).TenantID)
	httpx.JSON(w, http.StatusAccepted, run)
}
