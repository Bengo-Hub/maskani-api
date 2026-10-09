package works

import (
	"context"
	"strings"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/maskaniuseroutlet"
	"github.com/bengobox/maskani-api/internal/ent/party"
	"github.com/bengobox/maskani-api/internal/ent/unit"
	"github.com/bengobox/maskani-api/internal/modules/register"
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

// residentDetails adds to a resident request's event what the alert needs: the unit code, who
// asked, a short description and the staff assigned to the property in a responder role. With
// nobody assigned the alert goes to the estate's contact instead.
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
	rs, name, err := register.PropertyResponders(ctx, s.client, wo.PropertyID, responderRoles(wo.Category), maxResponders)
	if name != "" {
		payload["property"] = name
	}
	if err == nil && len(rs) > 0 {
		payload["responders"] = rs
	}
}
