package register

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/block"
	"github.com/bengobox/maskani-api/internal/ent/party"
	"github.com/bengobox/maskani-api/internal/ent/unit"
	"github.com/bengobox/maskani-api/internal/ent/unitparty"
	"github.com/bengobox/maskani-api/internal/shared/secure"
)

// Natural-key lookups shared by the CSV import and cmd/seed-tenant, so both find existing records
// the same way and re-running either never duplicates a block, unit, person or link.

// BlockByCode returns the property's block with this code, or nil when there is none.
func (s *Service) BlockByCode(ctx context.Context, propertyID uuid.UUID, code string) (*ent.Block, error) {
	b, err := s.client.Block.Query().Where(block.PropertyID(propertyID), block.Code(strings.ToUpper(strings.TrimSpace(code)))).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	return b, err
}

// EnsureBlock returns the block with this code, creating it when missing.
func (s *Service) EnsureBlock(ctx context.Context, propertyID uuid.UUID, code string) (*ent.Block, bool, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if b, err := s.BlockByCode(ctx, propertyID, code); err != nil || b != nil {
		return b, false, err
	}
	b, err := s.CreateBlock(ctx, propertyID, BlockInput{Code: code, Name: "Block " + code})
	return b, err == nil, err
}

// UnitByCode returns the property's unit with this code, or nil when there is none.
func (s *Service) UnitByCode(ctx context.Context, propertyID uuid.UUID, code string) (*ent.Unit, error) {
	u, err := s.client.Unit.Query().Where(unit.PropertyID(propertyID), unit.Code(strings.ToUpper(strings.TrimSpace(code)))).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	return u, err
}

// PartyByPhone finds a person by phone through the phone hash (phones are stored encrypted).
// Returns nil when there is no match or the phone does not parse.
func (s *Service) PartyByPhone(ctx context.Context, phone string) (*ent.Party, error) {
	n := secure.NormalizePhone(phone)
	if n == "" {
		return nil, nil
	}
	p, err := s.client.Party.Query().Where(party.PhoneHash(s.box.Hash(n))).First(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	return p, err
}

// HasActiveLink reports whether the person already holds this role on the unit.
func (s *Service) HasActiveLink(ctx context.Context, unitID, partyID uuid.UUID, role string) (bool, error) {
	return s.client.UnitParty.Query().Where(unitparty.UnitID(unitID), unitparty.PartyID(partyID),
		unitparty.RoleEQ(unitparty.Role(role)), unitparty.StatusEQ(unitparty.StatusActive)).Exist(ctx)
}
