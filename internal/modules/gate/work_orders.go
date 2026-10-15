package gate

import (
	"context"
	"maps"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"

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
func (s *Service) SetWorkOrderBooker(f WorkOrderBooker) { s.booker = f }

// requestMeta keeps a booking request on the entry or walk-in until it is booked.
func requestMeta(w *WorkOrderAtGate) map[string]any {
	if w == nil {
		return nil
	}
	return map[string]any{"work_order_request": map[string]any{"title": strings.TrimSpace(w.Title), "category": w.Category,
		"description": strings.TrimSpace(w.Description), "priority": w.Priority, "area": strings.TrimSpace(w.Area),
		"vendor_id": w.VendorID}}
}

// bookWorkOrders raises the work orders requested on entries just stored.
func (s *Service) bookWorkOrders(ctx context.Context, evs []*ent.GateEvent, passes map[uuid.UUID]*ent.VisitorPass) {
	for _, ev := range evs {
		if ev.Kind != gateevent.KindEntry {
			continue
		}
		var pass *ent.VisitorPass
		if ev.PassID != nil {
			pass = passes[*ev.PassID]
		}
		s.bookWorkOrder(ctx, ev, pass)
	}
}

// bookWorkOrder raises the work order an admitted visitor came for: on an entry, or on a walk-in
// once the host or the guard lets the provider in. An entry on a contractor pass that already
// belongs to a work order is linked to it instead of booking another. The work order id replaces
// the request on the event, so the office sees the visit beside the job and nothing books twice.
func (s *Service) bookWorkOrder(ctx context.Context, ev *ent.GateEvent, pass *ent.VisitorPass) {
	req, asked := ev.Metadata["work_order_request"].(map[string]any)
	var woID uuid.UUID
	if pass != nil && pass.WorkOrderID != nil {
		woID = *pass.WorkOrderID
	}
	if !asked && woID == uuid.Nil {
		return
	}
	if woID == uuid.Nil && s.booker != nil {
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
		var device uuid.UUID
		if ev.DeviceID != nil {
			device = *ev.DeviceID
		}
		id, err := s.booker(ctx, Booking{PropertyID: ev.PropertyID, UnitID: ev.HostUnitID, WorkOrder: w,
			ProviderName: ev.VisitorName, ProviderPhone: ev.VisitorPhone, GateEventID: ev.ID, DeviceID: device})
		if err != nil {
			s.log.Warn("work order not booked at the gate", zap.Error(err))
			return
		}
		woID = id
	}
	if woID == uuid.Nil {
		return
	}
	meta := map[string]any{}
	maps.Copy(meta, ev.Metadata)
	delete(meta, "work_order_request")
	meta["work_order_id"] = woID.String()
	if err := s.client.GateEvent.UpdateOneID(ev.ID).SetMetadata(meta).Exec(ctx); err == nil {
		ev.Metadata = meta
	}
}
