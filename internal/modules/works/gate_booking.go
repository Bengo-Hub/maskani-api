package works

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/vendor"
)

// GateBooking is a work order booked by the guard when a service provider arrives: for a unit (a
// resident's repair) or the estate in general (an area). Most providers are let in at the gate, so
// this is often the only record that a visit happened; the office follows up from it. A quote or
// cost is never needed to book; it can be added later or not at all.
type GateBooking struct {
	PropertyID    uuid.UUID
	UnitID        *uuid.UUID
	Area          string
	Category      string
	Title         string
	Description   string
	Priority      string
	VendorID      *uuid.UUID // a known vendor; else the provider's name and phone
	ProviderName  string
	ProviderPhone string
	GateEventID   uuid.UUID
	DeviceID      uuid.UUID
}

// BookFromGate raises the work order (source gate, responded at arrival) and returns it. A vendor
// id that is not an active vendor of this estate is dropped and the provider kept by name.
func (s *Service) BookFromGate(ctx context.Context, b GateBooking) (*ent.WorkOrder, error) {
	name := strings.TrimSpace(b.ProviderName)
	if b.VendorID != nil {
		v, err := s.client.Vendor.Query().Where(vendor.ID(*b.VendorID), vendor.StatusEQ(vendor.StatusActive)).Only(ctx)
		if err != nil {
			b.VendorID = nil
		} else if name == "" {
			name = v.Name
		}
	}
	if strings.TrimSpace(b.Category) == "" {
		b.Category = "general"
	}
	if strings.TrimSpace(b.Title) == "" {
		b.Title = "Visit by " + firstNonBlank(name, "a service provider")
	}
	if b.Area == "" && b.UnitID == nil {
		b.Area = "Estate"
	}
	meta := map[string]any{"booked_at_gate": true, "gate_event_id": b.GateEventID.String(), "gate_device_id": b.DeviceID.String(),
		"provider": map[string]any{"name": name, "phone": strings.TrimSpace(b.ProviderPhone)}}
	return s.Create(ctx, Actor{Kind: "gate"}, RequestInput{PropertyID: b.PropertyID, UnitID: b.UnitID, Area: b.Area,
		Category: b.Category, Priority: b.Priority, Title: b.Title, Description: b.Description, VendorID: b.VendorID, Meta: meta})
}

func firstNonBlank(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
