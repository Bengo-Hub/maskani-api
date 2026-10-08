package realtime

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestFilterAllows(t *testing.T) {
	p1, p2, u1 := uuid.NewString(), uuid.NewString(), uuid.NewString()
	limited := Filter{Staff: true, PropertyIDs: map[string]bool{p1: true}}
	if !limited.Allows(Event{Type: GateEvent, PropertyID: p1}) {
		t.Fatal("limited staff should see their property")
	}
	if limited.Allows(Event{Type: GateEvent, PropertyID: p2}) {
		t.Fatal("limited staff must not see another property")
	}
	if !limited.Allows(Event{Type: NoticeStatus}) {
		t.Fatal("tenant-wide events reach staff")
	}
	portal := Filter{UnitIDs: map[string]bool{u1: true}}
	if !portal.Allows(Event{Type: ReadingSaved, PropertyID: p2, UnitID: u1}) {
		t.Fatal("portal user should see their unit")
	}
	if portal.Allows(Event{Type: BillingRunProgress, PropertyID: p2}) || portal.Allows(Event{Type: NoticeStatus}) {
		t.Fatal("portal user must not see property or tenant events")
	}
}

func TestHubLocalDelivery(t *testing.T) {
	h := NewHub(nil, nil)
	tid := uuid.New()
	sub := h.Subscribe(tid)
	defer h.Unsubscribe(sub)
	other := h.Subscribe(uuid.New())
	defer h.Unsubscribe(other)
	var seen []string
	h.OnEvent(func(id uuid.UUID, ev Event) { seen = append(seen, ev.Type) })
	h.Publish(tid, Event{Type: PaymentApplied, ID: "x"})
	select {
	case <-sub.C:
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive the event")
	}
	select {
	case <-other.C:
		t.Fatal("event crossed tenants")
	default:
	}
	if len(seen) != 1 || seen[0] != PaymentApplied {
		t.Fatalf("OnEvent saw %v", seen)
	}
	var nilHub *Hub
	nilHub.Publish(tid, Event{Type: GateEvent}) // must not panic
	Emit(nil, tid, Event{Type: GateEvent})
}
