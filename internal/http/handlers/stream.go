package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/Bengo-Hub/httpware"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/platform/realtime"
)

// heartbeat keeps proxies from closing an idle stream (realtime-fanout standard: 15 seconds).
const heartbeat = 15 * time.Second

// Stream is GET /stream: server-sent change hints for the caller's tenant. Staff receive events
// for the properties they can see; portal users receive events for their own units only. Each
// frame is "event: <type>" plus a JSON {type, id, property_id, unit_id}; the client refetches.
func (h *H) Stream(w http.ResponseWriter, r *http.Request) {
	if h.RT == nil {
		httpx.Error(w, http.StatusServiceUnavailable, "unavailable", "live updates are not enabled")
		return
	}
	a := access(r)
	f := realtime.Filter{Staff: a.IsStaff(), AllProperties: a.AllProperties,
		PropertyIDs: map[string]bool{}, UnitIDs: map[string]bool{}}
	for _, p := range a.PropertyIDs {
		f.PropertyIDs[p.String()] = true
	}
	if len(a.PartyIDs) > 0 {
		units, err := h.Portal.UnitIDs(r.Context(), a.PartyIDs)
		if err != nil {
			httpx.Fail(w, err)
			return
		}
		for _, u := range units {
			f.UnitIDs[u.String()] = true
		}
	}
	if !f.Staff && len(f.UnitIDs) == 0 {
		httpx.Error(w, http.StatusForbidden, "forbidden", "no live updates for this account")
		return
	}

	httpware.StreamHeaders(w)
	httpware.ExtendWriteDeadline(w)
	rc := http.NewResponseController(w)
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprint(w, "retry: 5000\n: connected\n\n"); err != nil {
		return
	}
	_ = rc.Flush()

	sub := h.RT.Subscribe(a.TenantID)
	defer h.RT.Unsubscribe(sub)
	tick := time.NewTicker(heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			if rc.Flush() != nil {
				return
			}
		case raw, ok := <-sub.C:
			if !ok {
				return
			}
			ev, err := realtime.Decode(raw)
			if err != nil || !f.Allows(ev) {
				continue
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, raw); err != nil {
				return
			}
			if rc.Flush() != nil {
				return
			}
		}
	}
}
