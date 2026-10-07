// Package portal serves owners, buyers and occupants. Every read is scoped to the caller's own
// unit links on the server; an id alone never grants access (SRDD section 20).
package portal

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/notice"
	"github.com/bengobox/maskani-api/internal/ent/party"
	"github.com/bengobox/maskani-api/internal/ent/property"
	"github.com/bengobox/maskani-api/internal/ent/salecontract"
	"github.com/bengobox/maskani-api/internal/ent/unit"
	"github.com/bengobox/maskani-api/internal/ent/unitaccount"
	"github.com/bengobox/maskani-api/internal/ent/unitparty"
	"github.com/bengobox/maskani-api/internal/http/httpx"
)

// Service is the portal service.
type Service struct {
	client *ent.Client
	log    *zap.Logger
}

// NewService creates the portal service.
func NewService(client *ent.Client, log *zap.Logger) *Service {
	return &Service{client: client, log: log.Named("portal")}
}

// MyUnit is a unit the caller is linked to, with its accounts.
type MyUnit struct {
	Link     *ent.UnitParty     `json:"link"`
	Unit     *ent.Unit          `json:"unit"`
	Property *ent.Property      `json:"property"`
	Accounts []*ent.UnitAccount `json:"accounts"`
}

// Units returns the caller's active unit links.
func (s *Service) Units(ctx context.Context, partyIDs []uuid.UUID) ([]MyUnit, error) {
	links, err := s.client.UnitParty.Query().
		Where(unitparty.PartyIDIn(partyIDs...), unitparty.StatusEQ(unitparty.StatusActive)).
		WithUnit().Order(ent.Asc(unitparty.FieldStartDate)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := []MyUnit{}
	seen := map[uuid.UUID]bool{}
	for _, l := range links {
		if l.Edges.Unit == nil || seen[l.UnitID] {
			continue
		}
		seen[l.UnitID] = true
		mu := MyUnit{Link: l, Unit: l.Edges.Unit}
		mu.Property, _ = s.client.Property.Query().Where(property.ID(l.Edges.Unit.PropertyID)).Only(ctx)
		q := s.client.UnitAccount.Query().Where(unitaccount.UnitID(l.UnitID)).WithFund()
		if l.Role == unitparty.RoleOccupant || l.Role == unitparty.RoleHouseholdMember || l.Role == unitparty.RoleDomesticStaff {
			// Occupants see the estate account only when the owner assigned them charges.
			if len(l.BillTo) == 0 {
				q = q.Where(unitaccount.IDIn())
			}
		}
		mu.Accounts, _ = q.All(ctx)
		out = append(out, mu)
	}
	return out, nil
}

// OwnsAccount checks that the caller may see and pay an account.
func (s *Service) OwnsAccount(ctx context.Context, partyIDs []uuid.UUID, accountID uuid.UUID) error {
	acc, err := s.client.UnitAccount.Get(ctx, accountID)
	if err != nil {
		return err
	}
	ok, err := s.client.UnitParty.Query().Where(unitparty.UnitID(acc.UnitID), unitparty.PartyIDIn(partyIDs...),
		unitparty.StatusEQ(unitparty.StatusActive)).Exist(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return httpx.Forbidden("this account is not linked to you")
	}
	return nil
}

// OwnsUnit checks the caller is linked to the unit.
func (s *Service) OwnsUnit(ctx context.Context, partyIDs []uuid.UUID, unitID uuid.UUID) error {
	ok, err := s.client.UnitParty.Query().Where(unitparty.UnitID(unitID), unitparty.PartyIDIn(partyIDs...),
		unitparty.StatusEQ(unitparty.StatusActive)).Exist(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return httpx.Forbidden("this unit is not linked to you")
	}
	return nil
}

// Contracts returns the caller's purchase contracts.
func (s *Service) Contracts(ctx context.Context, partyIDs []uuid.UUID) ([]*ent.SaleContract, error) {
	return s.client.SaleContract.Query().Where(salecontract.PrimaryBuyerIDIn(partyIDs...),
		salecontract.StatusNotIn(salecontract.StatusDraft, salecontract.StatusCancelled)).All(ctx)
}

// Notices returns recent sent notices for the properties of the caller's units.
func (s *Service) Notices(ctx context.Context, partyIDs []uuid.UUID) ([]*ent.Notice, error) {
	props, err := s.client.Unit.Query().Where(unit.HasPartiesWith(unitparty.PartyIDIn(partyIDs...),
		unitparty.StatusEQ(unitparty.StatusActive))).Select(unit.FieldPropertyID).All(ctx)
	if err != nil {
		return nil, err
	}
	ids := []uuid.UUID{}
	for _, u := range props {
		ids = append(ids, u.PropertyID)
	}
	return s.client.Notice.Query().Where(notice.StatusEQ(notice.StatusSent),
		notice.Or(notice.PropertyIDIn(ids...), notice.PropertyIDIsNil()),
		notice.SentAtGT(time.Now().AddDate(0, -3, 0))).Order(ent.Desc(notice.FieldSentAt)).Limit(50).All(ctx)
}

// AcceptTerms records the terms and privacy version the caller accepted.
func (s *Service) AcceptTerms(ctx context.Context, partyIDs []uuid.UUID, version string) error {
	if version == "" {
		return httpx.Invalid("version is required")
	}
	_, err := s.client.Party.Update().Where(party.IDIn(partyIDs...)).
		SetTermsAcceptedVersion(version).SetTermsAcceptedAt(time.Now()).Save(ctx)
	return err
}
