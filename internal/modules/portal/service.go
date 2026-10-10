// Package portal serves owners, buyers and occupants. Every read is scoped to the caller's own
// unit links on the server; an id alone never grants access (SRDD section 20).
package portal

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/shopspring/decimal"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/meterreading"
	"github.com/bengobox/maskani-api/internal/ent/notice"
	"github.com/bengobox/maskani-api/internal/ent/party"
	"github.com/bengobox/maskani-api/internal/ent/salecontract"
	"github.com/bengobox/maskani-api/internal/ent/unit"
	"github.com/bengobox/maskani-api/internal/ent/unitaccount"
	"github.com/bengobox/maskani-api/internal/ent/unitparty"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/accounts"
	"github.com/bengobox/maskani-api/internal/modules/register"
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

// MyUnit is a unit the caller is linked to, with its accounts and last water reading.
type MyUnit struct {
	Link     *ent.UnitParty     `json:"link"`
	Unit     *ent.Unit          `json:"unit"`
	Property *ent.Property      `json:"property"`
	Accounts []*ent.UnitAccount `json:"accounts"`
	// Pay says how to pay each account at its fund's paybill, keyed by account id.
	Pay         map[uuid.UUID]accounts.PayInstruction `json:"pay"`
	LastReading *LastReading                          `json:"last_reading,omitempty"`
}

// LastReading is the unit meter's latest accepted reading, for the portal home (SRDD figure 14).
type LastReading struct {
	Period      string          `json:"period"`
	Reading     decimal.Decimal `json:"reading"`
	Consumption decimal.Decimal `json:"consumption"`
	ReadAt      time.Time       `json:"read_at"`
	Estimated   bool            `json:"estimated"`
	PhotoKey    string          `json:"photo_key,omitempty"`
}

// maxLinks bounds a caller's own links; one person never holds more units than this in an estate.
const maxLinks = 500

// activeLink is a link that grants access now (shared with the gate's host lookup).
var activeLink = register.ActiveLink

// isOccupantRole is a role that lives in or works at a unit without owning it.
func isOccupantRole(r unitparty.Role) bool {
	return r == unitparty.RoleOccupant || r == unitparty.RoleHouseholdMember || r == unitparty.RoleDomesticStaff
}

// accountVisible applies the bill-to rule (SRDD 8.3): owners, buyers and landlords see every
// account of the unit; an occupant sees the estate accounts only when the owner assigned them
// charges, and never the owner's purchase (sales fund) account.
func accountVisible(l *ent.UnitParty, a *ent.UnitAccount) bool {
	if !isOccupantRole(l.Role) {
		return true
	}
	if len(l.BillTo) == 0 {
		return false
	}
	return a.Edges.Fund == nil || a.Edges.Fund.Code != "sales"
}

// Units returns the caller's active unit links with their properties and visible accounts, in three
// queries whatever the number of units.
func (s *Service) Units(ctx context.Context, partyIDs []uuid.UUID) ([]MyUnit, error) {
	links, err := s.client.UnitParty.Query().
		Where(unitparty.PartyIDIn(partyIDs...), activeLink()).
		WithUnit(func(q *ent.UnitQuery) { q.WithProperty() }).
		Order(ent.Asc(unitparty.FieldStartDate)).Limit(maxLinks).All(ctx)
	if err != nil {
		return nil, err
	}
	unitIDs := make([]uuid.UUID, 0, len(links))
	for _, l := range links {
		unitIDs = append(unitIDs, l.UnitID)
	}
	accs, err := s.client.UnitAccount.Query().Where(unitaccount.UnitIDIn(unitIDs...)).WithFund().All(ctx)
	if err != nil {
		return nil, err
	}
	byUnit := map[uuid.UUID][]*ent.UnitAccount{}
	for _, a := range accs {
		byUnit[a.UnitID] = append(byUnit[a.UnitID], a)
	}
	// Latest accepted reading per unit over the last six months, in one bounded query.
	since := time.Now().AddDate(0, -6, 0).Format("2006-01")
	reads, err := s.client.MeterReading.Query().Where(meterreading.UnitIDIn(unitIDs...),
		meterreading.StatusEQ(meterreading.StatusAccepted), meterreading.PeriodGTE(since)).
		Order(ent.Desc(meterreading.FieldPeriod), ent.Desc(meterreading.FieldReadAt)).Limit(len(unitIDs)*8 + 1).All(ctx)
	if err != nil {
		return nil, err
	}
	lastRead := map[uuid.UUID]*LastReading{}
	for _, r := range reads {
		if r.UnitID == nil || lastRead[*r.UnitID] != nil {
			continue
		}
		lastRead[*r.UnitID] = &LastReading{Period: r.Period, Reading: r.Reading, Consumption: r.Consumption,
			ReadAt: r.ReadAt, Estimated: r.IsEstimated, PhotoKey: r.PhotoKey}
	}
	out := []MyUnit{}
	seen := map[uuid.UUID]bool{}
	for _, l := range links {
		u := l.Edges.Unit
		if u == nil || seen[l.UnitID] {
			continue
		}
		seen[l.UnitID] = true
		mu := MyUnit{Link: l, Unit: u, Property: u.Edges.Property, Accounts: []*ent.UnitAccount{},
			Pay: map[uuid.UUID]accounts.PayInstruction{}, LastReading: lastRead[l.UnitID]}
		for _, a := range byUnit[l.UnitID] {
			if accountVisible(l, a) {
				mu.Accounts = append(mu.Accounts, a)
				mu.Pay[a.ID] = accounts.PayInstructionFor(a.Edges.Fund, a.AccountRef)
			}
		}
		out = append(out, mu)
	}
	return out, nil
}

// UnitIDs returns the units the caller is actively linked to (bounded by the caller's own links).
func (s *Service) UnitIDs(ctx context.Context, partyIDs []uuid.UUID) ([]uuid.UUID, error) {
	if len(partyIDs) == 0 {
		return nil, nil
	}
	links, err := s.client.UnitParty.Query().
		Where(unitparty.PartyIDIn(partyIDs...), activeLink()).
		Select(unitparty.FieldUnitID).Limit(maxLinks).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]uuid.UUID, 0, len(links))
	for _, l := range links {
		out = append(out, l.UnitID)
	}
	return out, nil
}

// OwnsAccount checks that the caller may see and pay an account, under the same bill-to rule the
// portal home uses: an occupant cannot open the owner's accounts by guessing an id.
func (s *Service) OwnsAccount(ctx context.Context, partyIDs []uuid.UUID, accountID uuid.UUID) error {
	acc, err := s.client.UnitAccount.Query().Where(unitaccount.ID(accountID)).WithFund().Only(ctx)
	if err != nil {
		return err
	}
	links, err := s.client.UnitParty.Query().Where(unitparty.UnitID(acc.UnitID), unitparty.PartyIDIn(partyIDs...),
		activeLink()).Limit(maxLinks).All(ctx)
	if err != nil {
		return err
	}
	for _, l := range links {
		if accountVisible(l, acc) {
			return nil
		}
	}
	return httpx.Forbidden("this account is not linked to you")
}

// OwnsUnit checks the caller is linked to the unit.
func (s *Service) OwnsUnit(ctx context.Context, partyIDs []uuid.UUID, unitID uuid.UUID) error {
	ok, err := s.client.UnitParty.Query().Where(unitparty.UnitID(unitID), unitparty.PartyIDIn(partyIDs...),
		activeLink()).Exist(ctx)
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
		activeLink())).Unique(true).Select(unit.FieldPropertyID).Limit(maxLinks).All(ctx)
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

// AcceptedTerms returns the oldest terms version accepted across the caller's parties ("" when one
// of them has not accepted), so the portal asks again until every link has the current version.
func (s *Service) AcceptedTerms(ctx context.Context, partyIDs []uuid.UUID) string {
	if len(partyIDs) == 0 {
		return ""
	}
	rows, err := s.client.Party.Query().Where(party.IDIn(partyIDs...)).
		Select(party.FieldTermsAcceptedVersion).Strings(ctx)
	if err != nil || len(rows) == 0 {
		return ""
	}
	oldest := rows[0]
	for _, v := range rows[1:] {
		if v < oldest {
			oldest = v
		}
	}
	return oldest
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
