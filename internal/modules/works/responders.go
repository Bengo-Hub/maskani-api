package works

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/maskaniuser"
	"github.com/bengobox/maskani-api/internal/ent/maskaniuseroutlet"
	"github.com/bengobox/maskani-api/internal/ent/party"
	"github.com/bengobox/maskani-api/internal/ent/property"
	"github.com/bengobox/maskani-api/internal/ent/unit"
)

// maxResponders caps who is alerted about one request, so a large staff list never fans out.
const maxResponders = 10

// responderRoles are the property roles told about a resident request: the caretaker acts on it,
// the property manager oversees. A security request also reaches the property's security staff.
func responderRoles(category string) []maskaniuseroutlet.PropertyRole {
	roles := []maskaniuseroutlet.PropertyRole{maskaniuseroutlet.PropertyRoleCaretaker, maskaniuseroutlet.PropertyRolePropertyManager}
	if strings.Contains(strings.ToLower(category), "security") {
		roles = append(roles, maskaniuseroutlet.PropertyRoleSecurity)
	}
	return roles
}

// Responder is one staff member alerted about a resident request.
type Responder struct {
	UserID uuid.UUID `json:"user_id"` // auth-api user id: push devices are registered under it
	Name   string    `json:"name,omitempty"`
	Email  string    `json:"email,omitempty"`
	Phone  string    `json:"phone,omitempty"`
	Role   string    `json:"role"`
}

// residentDetails adds to a resident request's event what the alert needs: the unit code, who
// asked, a short description and the staff assigned to the property in a responder role (three
// reads). With nobody assigned the alert goes to the estate's contact instead.
func (s *Service) residentDetails(ctx context.Context, wo *ent.WorkOrder, payload map[string]any) {
	payload["source"] = string(wo.Source)
	payload["category"] = wo.Category
	desc := []rune(strings.TrimSpace(wo.Description))
	if len(desc) > 280 {
		desc = append(desc[:277], []rune("...")...)
	}
	payload["description"] = string(desc)
	if wo.UnitID != nil {
		if u, err := s.client.Unit.Query().Where(unit.ID(*wo.UnitID)).Select(unit.FieldCode).Only(ctx); err == nil {
			payload["unit_code"] = u.Code
		}
	}
	if wo.RequestedByPartyID != nil {
		if p, err := s.client.Party.Query().Where(party.ID(*wo.RequestedByPartyID)).Select(party.FieldDisplayName).Only(ctx); err == nil {
			payload["requested_by"] = p.DisplayName
		}
	}
	prop, err := s.client.Property.Query().Where(property.ID(wo.PropertyID)).Select(property.FieldOutletID, property.FieldName).Only(ctx)
	if err != nil {
		return
	}
	payload["property"] = prop.Name
	if prop.OutletID == nil {
		return
	}
	links, err := s.client.MaskaniUserOutlet.Query().
		Where(maskaniuseroutlet.OutletID(*prop.OutletID), maskaniuseroutlet.PropertyRoleIn(responderRoles(wo.Category)...)).
		Limit(maxResponders * 2).All(ctx)
	if err != nil || len(links) == 0 {
		return
	}
	role := make(map[uuid.UUID]string, len(links))
	ids := make([]uuid.UUID, 0, len(links))
	for _, l := range links {
		role[l.UserID] = string(l.PropertyRole)
		ids = append(ids, l.UserID)
	}
	users, err := s.client.MaskaniUser.Query().Where(maskaniuser.IDIn(ids...), maskaniuser.StatusEQ("active")).
		Limit(maxResponders).All(ctx)
	if err != nil {
		return
	}
	out := make([]Responder, 0, len(users))
	for _, u := range users {
		out = append(out, Responder{UserID: u.AuthServiceUserID, Name: u.Name, Email: u.Email, Phone: u.Phone, Role: role[u.ID]})
	}
	if len(out) > 0 {
		payload["responders"] = out
	}
}
