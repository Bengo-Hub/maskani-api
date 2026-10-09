package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

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

// DeviceWalkIn is GET /gate/walk-ins/{id}: the host's decision so far. {id} is the event id or the
// client_event_id the tablet generated (the only id it knows), always scoped to this device.
func (h *H) DeviceWalkIn(w http.ResponseWriter, r *http.Request) {
	id := chiParam(r, "id")
	if id == "" || len(id) > 64 {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	ev, err := h.Gate.DeviceEvent(r.Context(), device(r).ID, id)
	if err != nil || ev.PropertyID != device(r).PropertyID {
		httpx.Error(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"id": ev.ID, "decision": ev.Decision, "decided_at": ev.DecidedAt,
		"decided_by": ev.DecidedBy, "rings": ev.Metadata["rings"]})
}

// walkInID resolves {id} (server id or the tablet's client id) to a walk-in on this device.
func (h *H) walkInID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id := chiParam(r, "id")
	if id == "" || len(id) > 64 {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return uuid.Nil, false
	}
	ev, err := h.Gate.DeviceEvent(r.Context(), device(r).ID, id)
	if err != nil || ev.PropertyID != device(r).PropertyID {
		httpx.Error(w, http.StatusNotFound, "not_found", "not found")
		return uuid.Nil, false
	}
	return ev.ID, true
}

// DeviceResolveWalkIn is POST /gate/walk-ins/{id}/resolve {admit, note}: the guard lets the visitor
// in or turns them away, on the walk-in's own row (one log line, not two). Allowed at any time; a
// host's own answer stands.
func (h *H) DeviceResolveWalkIn(w http.ResponseWriter, r *http.Request) {
	id, ok := h.walkInID(w, r)
	if !ok {
		return
	}
	var in struct {
		Admit bool   `json:"admit"`
		Note  string `json:"note"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	ev, err := h.Gate.ResolveWalkIn(r.Context(), device(r), id, in.Admit, in.Note)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"id": ev.ID, "decision": ev.Decision, "decided_by": ev.DecidedBy, "decided_at": ev.DecidedAt})
}

// DeviceRingHost is POST /gate/walk-ins/{id}/ring: push to the host's phone plus WhatsApp again, at
// most every 30 seconds.
func (h *H) DeviceRingHost(w http.ResponseWriter, r *http.Request) {
	id, ok := h.walkInID(w, r)
	if !ok {
		return
	}
	ev, err := h.Gate.RingHost(r.Context(), device(r), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"id": ev.ID, "decision": ev.Decision, "rings": ev.Metadata["rings"]})
}

// DeviceInside is GET /gate/inside: who is inside now, for the exit picker.
func (h *H) DeviceInside(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Gate.Inside(r.Context(), device(r).PropertyID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// DeviceVisitors is GET /gate/visitors?q=: returning visitors matching a phone, plate or name.
func (h *H) DeviceVisitors(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Gate.LookupVisitors(r.Context(), device(r).PropertyID, r.URL.Query().Get("q"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
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

// DeviceSignOn is POST /gate/sign-on {badge, pin}: a guard starts a shift on this tablet.
func (h *H) DeviceSignOn(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Badge string `json:"badge"`
		PIN   string `json:"pin"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	res, err := h.Gate.SignOn(r.Context(), device(r), in.Badge, in.PIN)
	if errors.Is(err, gate.ErrSignOn) {
		httpx.Error(w, http.StatusUnauthorized, "invalid_credentials", err.Error())
		return
	}
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
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
