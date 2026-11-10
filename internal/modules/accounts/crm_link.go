package accounts

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Bengo-Hub/shared-service-client/crm"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/party"
	"github.com/bengobox/maskani-api/internal/ent/unitparty"
)

// crmLinkTimeout bounds the CRM call made while linking a party.
const crmLinkTimeout = 2 * time.Second

// billedRoles are the unit roles whose party receives bills, so the CRM holds their identity.
// Household members, domestic staff and emergency contacts are never sent to the CRM.
var billedRoles = []unitparty.Role{
	unitparty.RoleOwner, unitparty.RoleJointOwner, unitparty.RoleBuyer, unitparty.RoleOccupant, unitparty.RoleLandlord,
}

// IsBilledRole reports whether a party with this unit role is linked to the CRM.
func IsBilledRole(role string) bool {
	for _, r := range billedRoles {
		if string(r) == role {
			return true
		}
	}
	return false
}

// linkCRM gives a billed party its CRM contact when it has none. Only the name, phone and email
// are sent; identity numbers, KRA PIN, nationality and consents stay in maskani. Any failure is
// logged with ids only and leaves the party unlinked for the next Ensure to retry.
func (s *Service) linkCRM(ctx context.Context, unitID uuid.UUID, p *Party) {
	if s.crm == nil {
		return
	}
	billed, err := s.billed(ctx, unitID, p)
	if err != nil {
		s.log.Warn("crm link: role check failed", zap.String("party_id", p.ID.String()), zap.Error(err))
		return
	}
	if !billed {
		return
	}
	pt, err := s.client.Party.Get(ctx, p.ID)
	if err != nil {
		s.log.Warn("crm link: party not loaded", zap.String("party_id", p.ID.String()), zap.Error(err))
		return
	}
	if pt.CrmContactID != nil || pt.Status == party.StatusAnonymised {
		return
	}
	first, last := crmName(pt)
	// A CRM outage may hold up the property operation by at most crmLinkTimeout.
	cctx, cancel := context.WithTimeout(ctx, crmLinkTimeout)
	defer cancel()
	c, err := s.crm.UpsertStrict(cctx, crm.UpsertInput{
		TenantID: pt.TenantID, Phone: pt.Phone, Email: pt.Email, FirstName: first, LastName: last,
	})
	if err != nil {
		if errors.Is(err, crm.ErrDisabled) || errors.Is(err, crm.ErrNoIdentifier) {
			return
		}
		fields := append([]zap.Field{zap.String("tenant_id", pt.TenantID.String()), zap.String("party_id", pt.ID.String())},
			CRMErrorFields(err)...)
		s.log.Warn("crm link failed; the party stays unlinked until its next account update", fields...)
		return
	}
	if c.ID == uuid.Nil {
		return
	}
	// Only set when still empty, so a concurrent link never overwrites another.
	if err := s.client.Party.Update().Where(party.ID(pt.ID), party.CrmContactIDIsNil()).
		SetCrmContactID(c.ID).Exec(ctx); err != nil {
		s.log.Warn("crm link: contact id not saved", zap.String("party_id", pt.ID.String()),
			zap.String("contact_id", c.ID.String()), zap.Error(err))
	}
}

// billed reports whether the party has a billed role on the unit: the role the caller gave, or
// else any of its active links on the unit.
func (s *Service) billed(ctx context.Context, unitID uuid.UUID, p *Party) (bool, error) {
	if p.Role != "" {
		return IsBilledRole(p.Role), nil
	}
	return s.client.UnitParty.Query().Where(unitparty.UnitID(unitID), unitparty.PartyID(p.ID),
		unitparty.StatusEQ(unitparty.StatusActive), unitparty.RoleIn(billedRoles...)).Exist(ctx)
}

// crmName is the name sent to the CRM: a company's name whole, or a person's first and last name.
func crmName(p *ent.Party) (first, last string) {
	if p.Kind == party.KindCompany {
		name := strings.TrimSpace(p.CompanyName)
		if name == "" {
			name = strings.TrimSpace(p.DisplayName)
		}
		return name, ""
	}
	first, last = strings.TrimSpace(p.FirstName), strings.TrimSpace(p.LastName)
	if first == "" && last == "" {
		return crm.SplitName(p.DisplayName)
	}
	return first, last
}

// CRMErrorFields describes a failed CRM call for a log line without its message, which could
// echo personal data. A 400 or 422 is the CRM refusing the data; anything else is an outage.
func CRMErrorFields(err error) []zap.Field {
	var ce *crm.Error
	if errors.As(err, &ce) {
		outcome := "outage"
		if ce.Status == http.StatusBadRequest || ce.Status == http.StatusUnprocessableEntity {
			outcome = "refused"
		}
		return []zap.Field{zap.String("crm_outcome", outcome), zap.Int("crm_status", ce.Status), zap.String("crm_code", ce.Code)}
	}
	switch {
	case errors.Is(err, crm.ErrNotFound):
		return []zap.Field{zap.String("crm_outcome", "outage"), zap.Int("crm_status", http.StatusNotFound)}
	case errors.Is(err, crm.ErrDisabled):
		return []zap.Field{zap.String("crm_outcome", "disabled")}
	}
	return []zap.Field{zap.String("crm_outcome", "outage")}
}
