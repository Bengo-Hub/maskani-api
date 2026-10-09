package register

import (
	"context"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/maskaniuser"
	"github.com/bengobox/maskani-api/internal/ent/maskaniuseroutlet"
	"github.com/bengobox/maskani-api/internal/ent/property"
)

// Responder is a staff member assigned to a property, as an alert addresses them: notifications-api
// emails, WhatsApps and pushes each (push devices are registered under the auth user id).
type Responder struct {
	UserID uuid.UUID `json:"user_id"`
	Name   string    `json:"name,omitempty"`
	Email  string    `json:"email,omitempty"`
	Phone  string    `json:"phone,omitempty"`
	Role   string    `json:"role"`
}

// PropertyResponders returns up to max active staff assigned to the property in any of the given
// property roles, with the property's name (three small reads). Nobody assigned gives an empty
// list; the caller's alert then goes to the estate contact.
func PropertyResponders(ctx context.Context, client *ent.Client, propertyID uuid.UUID, roles []maskaniuseroutlet.PropertyRole, max int) ([]Responder, string, error) {
	prop, err := client.Property.Query().Where(property.ID(propertyID)).Select(property.FieldOutletID, property.FieldName).Only(ctx)
	if err != nil {
		return nil, "", err
	}
	if prop.OutletID == nil {
		return nil, prop.Name, nil
	}
	links, err := client.MaskaniUserOutlet.Query().
		Where(maskaniuseroutlet.OutletID(*prop.OutletID), maskaniuseroutlet.PropertyRoleIn(roles...)).
		Limit(max * 2).All(ctx)
	if err != nil || len(links) == 0 {
		return nil, prop.Name, err
	}
	role := make(map[uuid.UUID]string, len(links))
	ids := make([]uuid.UUID, 0, len(links))
	for _, l := range links {
		role[l.UserID] = string(l.PropertyRole)
		ids = append(ids, l.UserID)
	}
	users, err := client.MaskaniUser.Query().Where(maskaniuser.IDIn(ids...), maskaniuser.StatusEQ("active")).Limit(max).All(ctx)
	if err != nil {
		return nil, prop.Name, err
	}
	out := make([]Responder, 0, len(users))
	for _, u := range users {
		out = append(out, Responder{UserID: u.AuthServiceUserID, Name: u.Name, Email: u.Email, Phone: u.Phone, Role: role[u.ID]})
	}
	return out, prop.Name, nil
}
