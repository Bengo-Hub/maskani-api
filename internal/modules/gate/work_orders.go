package gate

import (
	"context"
	"maps"
	"strings"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/gateevent"
)

// WorkOrderAtGate is what the guard records about a service provider let in: the job, for a unit
// or an estate area, and the vendor or the provider's own name and phone. Quote and cost are never
// asked at the gate. It rides on the entry, so it works offline and is booked once even when the
// tablet replays its queue.
type WorkOrderAtGate struct {
	Title       string     `json:"title"`
	Category    string     `json:"category"`
	Description string     `json:"description"`
	Priority    string     `json:"priority"`
	Area        string     `json:"area"` // estate area when not for a unit, such as "Main pump house"
	VendorID    *uuid.UUID `json:"vendor_id"`
}

// Booking is a work order to raise from a gate entry (works.BookFromGate, wired in app.go so this
// module does not import works).
type Booking struct {
	PropertyID    uuid.UUID
	UnitID        *uuid.UUID
	WorkOrder     WorkOrderAtGate
	ProviderName  string
	ProviderPhone string
	GateEventID   uuid.UUID
	DeviceID      uuid.UUID
}

// WorkOrderBooker raises the work order and returns its id.
type WorkOrderBooker func(ctx context.Context, b Booking) (uuid.UUID, error)

// SetWorkOrderBooker wires work order booking at the gate.
func (s *Service) SetWorkOrderBooker(f WorkOrderBooker) { s.bookWorkOrder = f }

// requestMeta keeps a booking request on the entry until it is booked.
func requestMeta(w *WorkOrderAtGate) map[string]any {
	if w == nil {
		return nil
	}
	return map[string]any{"work_order_request": map[string]any{"title": strings.TrimSpace(w.Title), "category": w.Category,
		"description": strings.TrimSpace(w.Description), "priority": w.Priority, "area": strings.TrimSpace(w.Area),
		"vendor_id": w.VendorID}}
}

// bookWorkOrders raises the work orders requested on entries just stored. An entry on a contractor
// pass that already belongs to a work order is linked to it instead of booking another. The work
// order id is kept on the entry, so the office sees the visit beside the job.
func (s *Service) bookWorkOrders(ctx context.Context, d *ent.GateDevice, evs []*ent.GateEvent, passes map[uuid.UUID]*ent.VisitorPass) {
	for _, ev := range evs {
		req, ok := ev.Metadata["work_order_request"].(map[string]any)
		if !ok || ev.Kind != gateevent.KindEntry {
			continue
		}
		var woID uuid.UUID
		if ev.PassID != nil {
			if p := passes[*ev.PassID]; p != nil && p.WorkOrderID != nil {
				woID = *p.WorkOrderID
			}
		}
		if woID == uuid.Nil && s.bookWorkOrder != nil {
			w := WorkOrderAtGate{}
			w.Title, _ = req["title"].(string)
			w.Category, _ = req["category"].(string)
			w.Description, _ = req["description"].(string)
			w.Priority, _ = req["priority"].(string)
			w.Area, _ = req["area"].(string)
			if v, _ := req["vendor_id"].(string); v != "" {
				if id, err := uuid.Parse(v); err == nil {
					w.VendorID = &id
				}
			}
			id, err := s.bookWorkOrder(ctx, Booking{PropertyID: d.PropertyID, UnitID: ev.HostUnitID, WorkOrder: w,
				ProviderName: ev.VisitorName, ProviderPhone: ev.VisitorPhone, GateEventID: ev.ID, DeviceID: d.ID})
			if err != nil {
				s.log.Warn("work order not booked at the gate")
				continue
			}
			woID = id
		}
		if woID == uuid.Nil {
			continue
		}
		meta := maps.Clone(ev.Metadata)
		delete(meta, "work_order_request")
		meta["work_order_id"] = woID.String()
		_ = s.client.GateEvent.UpdateOneID(ev.ID).SetMetadata(meta).Exec(ctx)
	}
}
