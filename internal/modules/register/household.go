package register

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/unitparty"
	"github.com/bengobox/maskani-api/internal/ent/vehicle"
	"github.com/bengobox/maskani-api/internal/http/httpx"
)

// householdRoles are the people a resident manages themselves on the portal (SRDD 16.2 step 5).
var householdRoles = map[unitparty.Role]bool{unitparty.RoleHouseholdMember: true, unitparty.RoleDomesticStaff: true}

// Household is who lives or works at a unit besides its owners and occupants, and its vehicles.
type Household struct {
	People   []HouseholdPerson `json:"people"`
	Vehicles []*ent.Vehicle    `json:"vehicles"`
}

// HouseholdPerson is one household member or domestic staff link.
type HouseholdPerson struct {
	LinkID    uuid.UUID  `json:"link_id"`
	PartyID   uuid.UUID  `json:"party_id"`
	Name      string     `json:"name"`
	Phone     string     `json:"phone,omitempty"`
	Role      string     `json:"role"`
	StartDate time.Time  `json:"start_date"`
	EndDate   *time.Time `json:"end_date,omitempty"`
}

// HouseholdOf lists a unit's active household members, domestic staff and vehicles.
func (s *Service) HouseholdOf(ctx context.Context, unitID uuid.UUID) (*Household, error) {
	links, err := s.client.UnitParty.Query().Where(unitparty.UnitID(unitID),
		unitparty.RoleIn(unitparty.RoleHouseholdMember, unitparty.RoleDomesticStaff), ActiveLink()).
		WithParty().Order(ent.Asc(unitparty.FieldStartDate)).Limit(100).All(ctx)
	if err != nil {
		return nil, err
	}
	out := &Household{People: make([]HouseholdPerson, 0, len(links))}
	for _, l := range links {
		p := HouseholdPerson{LinkID: l.ID, PartyID: l.PartyID, Role: string(l.Role), StartDate: l.StartDate, EndDate: l.EndDate}
		if l.Edges.Party != nil {
			p.Name, p.Phone = l.Edges.Party.DisplayName, l.Edges.Party.Phone
		}
		out.People = append(out.People, p)
	}
	out.Vehicles, err = s.client.Vehicle.Query().Where(vehicle.UnitID(unitID), vehicle.StatusEQ(vehicle.StatusActive)).
		Order(ent.Asc(vehicle.FieldPlate)).Limit(50).All(ctx)
	return out, err
}

// HouseholdInput adds a household member or domestic staff from the portal.
type HouseholdInput struct {
	Name  string `json:"name"`
	Phone string `json:"phone"`
	Role  string `json:"role"` // household_member or domestic_staff
}

// AddHouseholdMember records the person (matched by phone when already known) and links them to the
// unit with the household role, from today. Domestic staff then get gate passes like any visitor.
func (s *Service) AddHouseholdMember(ctx context.Context, unitID, actor uuid.UUID, in HouseholdInput) (*ent.UnitParty, error) {
	role := unitparty.Role(in.Role)
	if !householdRoles[role] {
		return nil, httpx.Invalid("role must be household_member or domestic_staff")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 120 {
		return nil, httpx.Invalid("enter the person's name")
	}
	var phone *string
	if p := strings.TrimSpace(in.Phone); p != "" {
		phone = &p
	}
	p, err := s.CreateParty(ctx, actor, PartyInput{DisplayName: &name, Phone: phone})
	if err != nil {
		return nil, err
	}
	if n, err := s.client.UnitParty.Query().Where(unitparty.UnitID(unitID), unitparty.PartyID(p.ID), unitparty.RoleEQ(role), ActiveLink()).Count(ctx); err != nil {
		return nil, err
	} else if n > 0 {
		return nil, httpx.Conflict("this person is already on the unit")
	}
	if n, err := s.client.UnitParty.Query().Where(unitparty.UnitID(unitID),
		unitparty.RoleIn(unitparty.RoleHouseholdMember, unitparty.RoleDomesticStaff), ActiveLink()).Count(ctx); err != nil {
		return nil, err
	} else if n >= 30 {
		return nil, httpx.Conflict("a unit can list up to 30 household members and staff; remove someone first")
	}
	return s.LinkParty(ctx, unitID, actor, LinkInput{PartyID: p.ID, Role: string(role), Source: string(unitparty.SourcePortal)})
}

// EndHouseholdLink ends a household member's or domestic staff's link on the unit (from the portal),
// with the usual effects (their passes stop).
func (s *Service) EndHouseholdLink(ctx context.Context, unitID, linkID uuid.UUID) (*ent.UnitParty, error) {
	l, err := s.client.UnitParty.Get(ctx, linkID)
	if err != nil {
		return nil, err
	}
	if l.UnitID != unitID || !householdRoles[l.Role] {
		return nil, httpx.Forbidden("you can only remove household members and staff of your unit")
	}
	return s.EndLink(ctx, linkID, time.Now())
}

// RemoveVehicle takes a vehicle off a unit (the record stays, marked removed).
func (s *Service) RemoveVehicle(ctx context.Context, unitID, vehicleID uuid.UUID) (*ent.Vehicle, error) {
	v, err := s.client.Vehicle.Get(ctx, vehicleID)
	if err != nil {
		return nil, err
	}
	if v.UnitID != unitID {
		return nil, httpx.Forbidden("this vehicle is not on the unit")
	}
	return v.Update().SetStatus(vehicle.StatusRemoved).SetRemovedAt(time.Now()).Save(ctx)
}
