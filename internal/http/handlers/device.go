package handlers

import (
	"context"
	"net/http"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/gate"
)

// Gate tablet routes authenticate with X-Device-Key (registered by a manager) instead of a user
// token, so a tablet keeps working through shift changes and offline periods.

type deviceKey struct{}

// DeviceAuth resolves the device and scopes the request to its tenant.
func (h *H) DeviceAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d, ctx, err := h.Gate.Device(r.Context(), r.Header.Get("X-Device-Key"))
		if err != nil {
			httpx.Fail(w, err)
			return
		}
		ctx = context.WithValue(ctx, deviceKey{}, d)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func device(r *http.Request) *ent.GateDevice {
	d, _ := r.Context().Value(deviceKey{}).(*ent.GateDevice)
	return d
}

// DeviceVerify is POST /gate/verify {code|qr}.
func (h *H) DeviceVerify(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
		QR   string `json:"qr"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	res, err := h.Gate.Verify(r.Context(), device(r), in.Code, in.QR)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

// DeviceEvents is POST /gate/events {events:[...]} (online and offline replay).
func (h *H) DeviceEvents(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Events []gate.EventInput `json:"events"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	if len(in.Events) > 500 {
		httpx.Error(w, http.StatusUnprocessableEntity, "validation_failed", "at most 500 events per upload")
		return
	}
	n, err := h.Gate.Record(r.Context(), device(r), in.Events)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]int{"stored": n, "received": len(in.Events)})
}

// DeviceSync is GET /gate/sync: the 24 hour offline cache.
func (h *H) DeviceSync(w http.ResponseWriter, r *http.Request) {
	d := device(r)
	payload, err := h.Gate.Sync(r.Context(), d)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"device": map[string]any{"id": d.ID, "name": d.Name,
		"gate_name": d.GateName, "property_id": d.PropertyID}, "cache": payload})
}

// DeviceWalkIn is GET /gate/walk-ins/{id}: the host's decision so far.
func (h *H) DeviceWalkIn(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	ev, err := h.Gate.Event(r.Context(), id)
	if err != nil || ev.PropertyID != device(r).PropertyID {
		httpx.Error(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"id": ev.ID, "decision": ev.Decision, "decided_at": ev.DecidedAt})
}

// DeviceIncident is POST /gate/incidents.
func (h *H) DeviceIncident(w http.ResponseWriter, r *http.Request) {
	var in gate.IncidentInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	d := device(r)
	in.PropertyID = d.PropertyID
	inc, err := h.Gate.ReportIncident(r.Context(), "guard", d.ID, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, inc)
}

// DeviceUnits is GET /gate/units: unit codes for the walk-in host picker.
func (h *H) DeviceUnits(w http.ResponseWriter, r *http.Request) {
	pid := device(r).PropertyID
	rows, err := h.Register.ListUnitCodes(r.Context(), pid)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}
