package register

import (
	"context"
	"strings"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/maskaniuser"
	"github.com/bengobox/maskani-api/internal/ent/maskaniuseroutlet"
	"github.com/bengobox/maskani-api/internal/ent/party"
	"github.com/bengobox/maskani-api/internal/ent/unit"
	"github.com/bengobox/maskani-api/internal/ent/unitparty"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/accounts"
	"github.com/bengobox/maskani-api/internal/modules/authapi"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
	"github.com/bengobox/maskani-api/internal/shared/page"
	"github.com/bengobox/maskani-api/internal/shared/secure"
)

// PartyInput is the create and update body. Identity numbers arrive in clear and are encrypted.
type PartyInput struct {
	Kind               *string        `json:"kind"`
	DisplayName        *string        `json:"display_name"`
	FirstName          *string        `json:"first_name"`
	LastName           *string        `json:"last_name"`
	CompanyName        *string        `json:"company_name"`
	RegistrationNumber *string        `json:"registration_number"`
	Phone              *string        `json:"phone"`
	AltPhone           *string        `json:"alt_phone"`
	Email              *string        `json:"email"`
	IDType             *string        `json:"id_type"`
	NationalID         *string        `json:"national_id"`
	KRAPin             *string        `json:"kra_pin"`
	Nationality        *string        `json:"nationality"`
	IsTaxResident      *bool          `json:"is_tax_resident"`
	IsDiaspora         *bool          `json:"is_diaspora"`
	PostalAddress      *string        `json:"postal_address"`
	PreferredChannel   *string        `json:"preferred_channel"`
	Notes              *string        `json:"notes"`
	CustomFields       map[string]any `json:"custom_fields"`
}

func (in PartyInput) name() string {
	if in.DisplayName != nil && strings.TrimSpace(*in.DisplayName) != "" {
		return strings.TrimSpace(*in.DisplayName)
	}
	if in.CompanyName != nil && strings.TrimSpace(*in.CompanyName) != "" {
		return strings.TrimSpace(*in.CompanyName)
	}
	parts := []string{}
	if in.FirstName != nil {
		parts = append(parts, strings.TrimSpace(*in.FirstName))
	}
	if in.LastName != nil {
		parts = append(parts, strings.TrimSpace(*in.LastName))
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

// CreateParty records a party, de-duplicating by phone within the tenant.
func (s *Service) CreateParty(ctx context.Context, actor uuid.UUID, in PartyInput) (*ent.Party, error) {
	name := in.name()
	if name == "" {
		return nil, httpx.Invalid("a name is required")
	}
	phone := ""
	if in.Phone != nil && *in.Phone != "" {
		phone = secure.NormalizePhone(*in.Phone)
		if phone == "" {
			return nil, httpx.Invalid("phone number is not valid")
		}
		if existing, err := s.client.Party.Query().Where(party.PhoneHash(s.box.Hash(phone))).First(ctx); err == nil {
			return existing, nil
		}
	}
	c := s.client.Party.Create().SetDisplayName(name).SetCreatedBy(actor)
	if phone != "" {
		c.SetPhone(phone).SetPhoneHash(s.box.Hash(phone))
	}
	if err := s.applyParty(c.Mutation(), in); err != nil {
		return nil, err
	}
	return c.Save(ctx)
}

// UpdateParty applies a partial update.
func (s *Service) UpdateParty(ctx context.Context, id uuid.UUID, in PartyInput) (*ent.Party, error) {
	u := s.client.Party.UpdateOneID(id)
	if n := in.name(); n != "" && (in.DisplayName != nil || in.FirstName != nil || in.CompanyName != nil) {
		u.SetDisplayName(n)
	}
	if in.Phone != nil {
		phone := secure.NormalizePhone(*in.Phone)
		if phone == "" && *in.Phone != "" {
			return nil, httpx.Invalid("phone number is not valid")
		}
		u.SetPhone(phone).SetPhoneHash(s.box.Hash(phone))
	}
	if err := s.applyParty(u.Mutation(), in); err != nil {
		return nil, err
	}
	return u.Save(ctx)
}

func (s *Service) applyParty(m *ent.PartyMutation, in PartyInput) error {
	if in.Kind != nil {
		m.SetKind(party.Kind(*in.Kind))
	}
	if in.FirstName != nil {
		m.SetFirstName(*in.FirstName)
	}
	if in.LastName != nil {
		m.SetLastName(*in.LastName)
	}
	if in.CompanyName != nil {
		m.SetCompanyName(*in.CompanyName)
	}
	if in.RegistrationNumber != nil {
		m.SetRegistrationNumber(*in.RegistrationNumber)
	}
	if in.AltPhone != nil {
		m.SetAltPhone(secure.NormalizePhone(*in.AltPhone))
	}
	if in.Email != nil {
		m.SetEmail(strings.ToLower(strings.TrimSpace(*in.Email)))
	}
	if in.IDType != nil {
		m.SetIDType(party.IDType(*in.IDType))
	}
	if in.NationalID != nil {
		enc, err := s.box.Encrypt(strings.TrimSpace(*in.NationalID))
		if err != nil {
			return err
		}
		m.SetNationalIDEnc(enc)
	}
	if in.KRAPin != nil {
		enc, err := s.box.Encrypt(strings.ToUpper(strings.TrimSpace(*in.KRAPin)))
		if err != nil {
			return err
		}
		m.SetKraPinEnc(enc)
	}
	if in.Nationality != nil {
		m.SetNationality(*in.Nationality)
	}
	if in.IsTaxResident != nil {
		m.SetIsTaxResident(*in.IsTaxResident)
	}
	if in.IsDiaspora != nil {
		m.SetIsDiaspora(*in.IsDiaspora)
	}
	if in.PostalAddress != nil {
		m.SetPostalAddress(*in.PostalAddress)
	}
	if in.PreferredChannel != nil {
		m.SetPreferredChannel(party.PreferredChannel(*in.PreferredChannel))
	}
	if in.Notes != nil {
		m.SetNotes(*in.Notes)
	}
	if in.CustomFields != nil {
		m.SetCustomFields(in.CustomFields)
	}
	return nil
}

// PartyView hides encrypted fields and shows masked identity values to callers with the right to see them.
type PartyView struct {
	*ent.Party
	NationalIDMasked string `json:"national_id_masked,omitempty"`
	KRAPinMasked     string `json:"kra_pin_masked,omitempty"`
}

// View builds the API view of a party.
func (s *Service) View(p *ent.Party) PartyView {
	v := PartyView{Party: p}
	if id, err := s.box.Decrypt(p.NationalIDEnc); err == nil && id != "" {
		v.NationalIDMasked = secure.Mask(id)
	}
	if pin, err := s.box.Decrypt(p.KraPinEnc); err == nil && pin != "" {
		v.KRAPinMasked = secure.Mask(pin)
	}
	return v
}

// ListParties returns a keyset page, searchable by name prefix or phone.
func (s *Service) ListParties(ctx context.Context, q string, p page.Params) (page.Result[PartyView], error) {
	query := s.client.Party.Query().Where(party.StatusNEQ(party.StatusAnonymised))
	if t := strings.TrimSpace(q); t != "" {
		if ph := secure.NormalizePhone(t); ph != "" {
			query = query.Where(party.PhoneHash(s.box.Hash(ph)))
		} else {
			query = query.Where(func(sel *sql.Selector) {
				sel.Where(sql.ContainsFold(sel.C(party.FieldDisplayName), t))
			})
		}
	}
	rows, err := query.Where(p.Predicate()).Modify(page.Order()).Limit(p.Limit + 1).All(ctx)
	if err != nil {
		return page.Result[PartyView]{}, err
	}
	res := page.Build(rows, p.Limit, func(x *ent.Party) (uuid.UUID, time.Time) { return x.ID, x.CreatedAt })
	out := make([]PartyView, len(res.Data))
	for i, x := range res.Data {
		out[i] = s.View(x)
	}
	return page.Result[PartyView]{Data: out, NextCursor: res.NextCursor, HasMore: res.HasMore}, nil
}

// LinkInput attaches a party to a unit.
type LinkInput struct {
	PartyID        uuid.UUID  `json:"party_id"`
	Role           string     `json:"role"`
	OwnershipShare *float64   `json:"ownership_share"`
	IsPrimary      bool       `json:"is_primary"`
	StartDate      *time.Time `json:"start_date"`
	EndDate        *time.Time `json:"end_date"`
	BillTo         []string   `json:"bill_to"`
	Source         string     `json:"source"`
}

// LinkParty records a dated relationship. Linking an owner opens the unit's estate account in the
// owner's name; linking an occupant marks the unit occupied.
func (s *Service) LinkParty(ctx context.Context, unitID, actor uuid.UUID, in LinkInput) (*ent.UnitParty, error) {
	u, err := s.client.Unit.Get(ctx, unitID)
	if err != nil {
		return nil, err
	}
	p, err := s.client.Party.Get(ctx, in.PartyID)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	if in.StartDate != nil {
		start = *in.StartDate
	}
	c := s.client.UnitParty.Create().SetUnitID(unitID).SetPartyID(p.ID).
		SetRole(unitparty.Role(in.Role)).SetIsPrimary(in.IsPrimary).SetStartDate(start).SetCreatedBy(actor)
	if in.OwnershipShare != nil {
		c.SetOwnershipShare(decimal.NewFromFloat(*in.OwnershipShare))
	}
	if in.EndDate != nil {
		c.SetEndDate(*in.EndDate)
	}
	if in.BillTo != nil {
		c.SetBillTo(in.BillTo)
	}
	if in.Source != "" {
		c.SetSource(unitparty.Source(in.Source))
	}
	link, err := c.Save(ctx)
	if err != nil {
		return nil, err
	}
	switch link.Role {
	case unitparty.RoleOwner, unitparty.RoleJointOwner:
		if in.IsPrimary || link.Role == unitparty.RoleOwner {
			if _, err := s.accounts.Ensure(ctx, u, "estate", &accounts.Party{ID: p.ID, Name: p.DisplayName, Phone: p.Phone}); err != nil {
				s.log.Warn("estate account not opened", zap.Error(err))
			}
		}
		if u.OccupancyStatus == unit.OccupancyStatusVacant {
			_ = u.Update().SetOccupancyStatus(unit.OccupancyStatusOwnerOccupied).Exec(ctx)
		}
	case unitparty.RoleOccupant:
		_ = u.Update().SetOccupancyStatus(unit.OccupancyStatusTenanted).Exec(ctx)
	}
	return link, nil
}

// EndLink closes a relationship on a date (transfer, move-out).
func (s *Service) EndLink(ctx context.Context, linkID uuid.UUID, end time.Time) (*ent.UnitParty, error) {
	return s.client.UnitParty.UpdateOneID(linkID).SetEndDate(end).SetStatus(unitparty.StatusEnded).Save(ctx)
}

// Invite links the party to an auth-api account by phone (creating one if needed) with a portal
// role, and publishes party.invited so notifications sends the portal SMS.
func (s *Service) Invite(ctx context.Context, partyID uuid.UUID, tenantSlug, portalURL string) (*ent.Party, error) {
	p, err := s.client.Party.Get(ctx, partyID)
	if err != nil {
		return nil, err
	}
	if p.Phone == "" {
		return nil, httpx.Invalid("the party has no phone number")
	}
	role := "maskani_owner"
	if n, _ := s.client.UnitParty.Query().Where(unitparty.PartyID(p.ID), unitparty.StatusEQ(unitparty.StatusActive),
		unitparty.RoleIn(unitparty.RoleOwner, unitparty.RoleJointOwner, unitparty.RoleBuyer)).Count(ctx); n == 0 {
		role = "maskani_occupant"
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	res, err := s.auth.AddMember(ctx, tenantID, authapi.MemberRequest{Phone: "+" + p.Phone, Email: p.Email, Name: p.DisplayName, Roles: []string{role}})
	if err != nil {
		return nil, err
	}
	authID, err := uuid.Parse(res.UserID)
	if err != nil {
		return nil, err
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	p, err = tx.Party.UpdateOneID(p.ID).SetAuthUserID(authID).SetInvitedAt(time.Now()).Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := events.Publish(ctx, tx.OutboxEvent, tenantID, p.ID.String(), events.PartyInvited, map[string]any{
		"party_id": p.ID, "name": p.DisplayName, "phone": p.Phone, "tenant_slug": tenantSlug,
		"portal_url": portalURL, "role": role,
	}); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return p, tx.Commit()
}

// AssignStaff assigns a staff user to a property (its outlet) with a property role.
func (s *Service) AssignStaff(ctx context.Context, propertyID, authUserID, actor uuid.UUID, role, erpEmployeeID string) (*ent.MaskaniUserOutlet, error) {
	prop, err := s.client.Property.Get(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	if prop.OutletID == nil {
		return nil, httpx.Invalid("this property has no outlet yet; create it again or link it in auth")
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	user, err := s.client.MaskaniUser.Query().
		Where(maskaniuser.TenantID(tenantID), maskaniuser.AuthServiceUserID(authUserID)).Only(ctx)
	if err != nil {
		return nil, httpx.Invalid("the user has not signed in to Maskani yet")
	}
	c := s.client.MaskaniUserOutlet.Create().SetTenantID(tenantID).SetUserID(user.ID).SetOutletID(*prop.OutletID).
		SetPropertyRole(maskaniuseroutlet.PropertyRole(role)).SetAssignedBy(actor)
	if erpEmployeeID != "" {
		c.SetErpEmployeeID(erpEmployeeID)
	}
	id, err := c.OnConflictColumns(maskaniuseroutlet.FieldTenantID, maskaniuseroutlet.FieldUserID, maskaniuseroutlet.FieldOutletID).
		UpdatePropertyRole().UpdateErpEmployeeID().ID(ctx)
	if err != nil {
		return nil, err
	}
	return s.client.MaskaniUserOutlet.Get(ctx, id)
}

// ListStaff returns the property's staff assignments.
func (s *Service) ListStaff(ctx context.Context, propertyID uuid.UUID) ([]*ent.MaskaniUserOutlet, error) {
	prop, err := s.client.Property.Get(ctx, propertyID)
	if err != nil || prop.OutletID == nil {
		return []*ent.MaskaniUserOutlet{}, err
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	return s.client.MaskaniUserOutlet.Query().
		Where(maskaniuseroutlet.TenantID(tenantID), maskaniuseroutlet.OutletID(*prop.OutletID)).All(ctx)
}

// RemoveStaff deletes an assignment.
func (s *Service) RemoveStaff(ctx context.Context, assignmentID uuid.UUID) error {
	tenantID, _ := tenantguard.TenantID(ctx)
	_, err := s.client.MaskaniUserOutlet.Delete().
		Where(maskaniuseroutlet.ID(assignmentID), maskaniuseroutlet.TenantID(tenantID)).Exec(ctx)
	return err
}

// AddVehicle registers a vehicle plate for a unit.
func (s *Service) AddVehicle(ctx context.Context, unitID uuid.UUID, partyID *uuid.UUID, plate, make_, model, colour string) (*ent.Vehicle, error) {
	plate = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(plate), " ", ""))
	if plate == "" {
		return nil, httpx.Invalid("plate is required")
	}
	c := s.client.Vehicle.Create().SetUnitID(unitID).SetPlate(plate).SetMake(make_).SetModel(model).SetColour(colour)
	if partyID != nil {
		c.SetPartyID(*partyID)
	}
	return c.Save(ctx)
}
