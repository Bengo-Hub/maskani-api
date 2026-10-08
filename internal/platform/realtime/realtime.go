// Package realtime pushes small change hints to connected browsers over SSE. Services publish an
// Event after their database commit; every replica relays it to its own subscribers through the
// shared-events Broadcaster (core NATS, subject _rt.maskani.events.<tenant>.<scope>), so a client
// connected to any pod hears about a write made on any other pod. Payloads are hints only: the
// client refetches through the normal, permission-checked endpoints.
package realtime

import (
	"encoding/json"

	eventslib "github.com/Bengo-Hub/shared-events"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
)

// Event types (the SSE "event:" field).
const (
	BillingRunProgress = "billing_run.progress"
	PaymentApplied     = "payment.applied"
	WorkOrderUpdated   = "work_order.updated"
	GateEvent          = "gate.event"
	WalkInRequested    = "walk_in.requested"
	WalkInDecided      = "walk_in.decided"
	ReadingSaved       = "reading.saved"
	NoticeStatus       = "notice.status"
)

// Namespace and Topic make the relay subject _rt.maskani.events.<tenant>.<scope>.
const (
	Namespace = "maskani"
	Topic     = "events"
)

// Event is the SSE payload.
type Event struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	PropertyID string `json:"property_id,omitempty"`
	UnitID     string `json:"unit_id,omitempty"`
}

// Publisher is what domain services depend on, so none of them imports NATS.
type Publisher interface {
	Publish(tenantID uuid.UUID, ev Event)
}

// Emit publishes through p when it is set; services call this so a nil publisher is a no-op.
func Emit(p Publisher, tenantID uuid.UUID, ev Event) {
	if p == nil || tenantID == uuid.Nil || ev.Type == "" {
		return
	}
	p.Publish(tenantID, ev)
}

// IDString formats an optional id ("" when nil).
func IDString(id *uuid.UUID) string {
	if id == nil || *id == uuid.Nil {
		return ""
	}
	return id.String()
}

// Hub is the per-pod registry of SSE subscribers on top of the shared FanoutHub.
type Hub struct {
	fan   *eventslib.FanoutHub
	relay *eventslib.Broadcaster
	log   *zap.Logger
}

// NewHub builds the hub. nc may be nil (local development, tests): delivery is then this pod only.
func NewHub(log *zap.Logger, nc *nats.Conn) *Hub {
	if log == nil {
		log = zap.NewNop()
	}
	relay := eventslib.NewBroadcaster(log, nc, Namespace)
	fan, err := eventslib.NewFanoutHub(relay, Topic, 64)
	if err != nil {
		log.Warn("realtime relay subscription failed; delivery is local to this pod", zap.Error(err))
	}
	return &Hub{fan: fan, relay: relay, log: log.Named("realtime")}
}

// Publish sends the event to every subscriber of the tenant on every pod. Nil-safe.
func (h *Hub) Publish(tenantID uuid.UUID, ev Event) {
	if h == nil || h.fan == nil {
		return
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	h.fan.Publish(tenantID.String(), "", b)
}

// Subscribe registers one SSE connection for a tenant. Filtering by property or unit happens in
// the handler, which knows the caller's access.
func (h *Hub) Subscribe(tenantID uuid.UUID) *eventslib.Sub {
	return h.fan.Subscribe(tenantID.String())
}

// Unsubscribe removes a connection.
func (h *Hub) Unsubscribe(s *eventslib.Sub) { h.fan.Unsubscribe(s) }

// OnEvent runs fn for every event published on any pod (cache invalidation). fn must not block.
func (h *Hub) OnEvent(fn func(tenantID uuid.UUID, ev Event)) {
	if h == nil || h.relay == nil {
		return
	}
	_ = h.relay.Subscribe(Topic, func(m eventslib.BroadcastMessage) {
		tid, err := uuid.Parse(m.TenantID)
		if err != nil {
			return
		}
		var ev Event
		if json.Unmarshal(m.Data, &ev) == nil {
			fn(tid, ev)
		}
	})
}

// Decode parses a relayed payload.
func Decode(raw []byte) (Event, error) {
	var ev Event
	err := json.Unmarshal(raw, &ev)
	return ev, err
}

// Filter decides whether a connection may see an event.
type Filter struct {
	Staff         bool
	AllProperties bool
	PropertyIDs   map[string]bool
	UnitIDs       map[string]bool
}

// Allows reports whether the event reaches this connection: staff by property access (events with
// no property reach every staff member), portal users only for their own units.
func (f Filter) Allows(ev Event) bool {
	if f.Staff {
		if f.AllProperties || ev.PropertyID == "" || f.PropertyIDs[ev.PropertyID] {
			return true
		}
	}
	return ev.UnitID != "" && f.UnitIDs[ev.UnitID]
}
