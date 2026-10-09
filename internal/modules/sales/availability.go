package sales

import (
	"context"
	"sort"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/block"
	"github.com/bengobox/maskani-api/internal/ent/unit"
)

// AvailabilityUnit is one tile of the availability board: only what the tile and its sheet show.
type AvailabilityUnit struct {
	ID             uuid.UUID        `json:"id"`
	Code           string           `json:"code"`
	UnitType       string           `json:"unit_type,omitempty"`
	Bedrooms       *int             `json:"bedrooms,omitempty"`
	SizeSqm        *decimal.Decimal `json:"size_sqm,omitempty"`
	Phase          string           `json:"phase,omitempty"`
	SaleStatus     unit.SaleStatus  `json:"sale_status"`
	Price          *decimal.Decimal `json:"price,omitempty"`
	ReservationFee *decimal.Decimal `json:"reservation_fee,omitempty"`
	DepositPct     float64          `json:"deposit_pct,omitempty"`
	PriceItemID    *uuid.UUID       `json:"price_list_item_id,omitempty"`
}

// AvailabilityGroup is one block (or phase when units have no block) of the board.
type AvailabilityGroup struct {
	Name      string             `json:"name"`
	Available int                `json:"available"`
	Units     []AvailabilityUnit `json:"units"`
}

// Availability returns the property's units for sale grouped by block, blocks in their set order and
// units in natural code order (A2 before A10). The board shows a whole property at once, so it is
// not paged: it is bounded by the property's unit count and each tile carries only its own fields.
// status, already validated by the caller, narrows it to one sale status (for example "available").
func (s *Service) Availability(ctx context.Context, propertyID uuid.UUID, status string) ([]AvailabilityGroup, error) {
	q := s.client.Unit.Query().Where(unit.PropertyID(propertyID), unit.StatusEQ(unit.StatusActive),
		unit.SaleStatusNEQ(unit.SaleStatusNotForSale))
	if status != "" {
		q = q.Where(unit.SaleStatusEQ(unit.SaleStatus(status)))
	}
	units, err := q.Select(unit.FieldID, unit.FieldBlockID, unit.FieldCode, unit.FieldUnitType, unit.FieldBedrooms,
		unit.FieldSizeSqm, unit.FieldPhase, unit.FieldSaleStatus).All(ctx)
	if err != nil {
		return nil, err
	}
	blocks, err := s.client.Block.Query().Where(block.PropertyID(propertyID)).
		Select(block.FieldID, block.FieldCode, block.FieldName, block.FieldSort).All(ctx)
	if err != nil {
		return nil, err
	}
	items, err := s.activeItems(ctx, propertyID)
	if err != nil {
		return nil, err
	}

	byBlock := make(map[uuid.UUID]*ent.Block, len(blocks))
	for _, b := range blocks {
		byBlock[b.ID] = b
	}
	type groupKey struct {
		sort int
		name string
	}
	groups := map[string]*AvailabilityGroup{}
	keys := map[string]groupKey{}
	for _, u := range units {
		name, order := "Units", 1<<30
		if b := blockOf(byBlock, u.BlockID); b != nil {
			name, order = b.Code, b.Sort
			if b.Name != "" {
				name = b.Name
			}
		} else if u.Phase != "" {
			name, order = u.Phase, 1<<29
		}
		g := groups[name]
		if g == nil {
			g = &AvailabilityGroup{Name: name}
			groups[name], keys[name] = g, groupKey{order, name}
		}
		row := AvailabilityUnit{ID: u.ID, Code: u.Code, UnitType: u.UnitType, Bedrooms: u.Bedrooms, SizeSqm: u.SizeSqm,
			Phase: u.Phase, SaleStatus: u.SaleStatus}
		if it := pickItem(items, u); it != nil {
			row.Price, row.ReservationFee, row.DepositPct, row.PriceItemID = &it.Price, &it.ReservationFee, it.DepositPct, &it.ID
		}
		if u.SaleStatus == unit.SaleStatusAvailable {
			g.Available++
		}
		g.Units = append(g.Units, row)
	}

	out := make([]AvailabilityGroup, 0, len(groups))
	for _, g := range groups {
		sort.Slice(g.Units, func(i, j int) bool { return naturalLess(g.Units[i].Code, g.Units[j].Code) })
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := keys[out[i].Name], keys[out[j].Name]
		if a.sort != b.sort {
			return a.sort < b.sort
		}
		return naturalLess(a.name, b.name)
	})
	return out, nil
}

// ValidSaleStatus reports whether s is a unit sale status.
func ValidSaleStatus(s string) bool {
	return unit.SaleStatusValidator(unit.SaleStatus(s)) == nil
}

func blockOf(m map[uuid.UUID]*ent.Block, id *uuid.UUID) *ent.Block {
	if id == nil {
		return nil
	}
	return m[*id]
}

// naturalLess orders codes the way people read them: digit runs compare as numbers, so "A2" comes
// before "A10"; letters compare without case.
func naturalLess(a, b string) bool {
	ra, rb := []rune(a), []rune(b)
	i, j := 0, 0
	for i < len(ra) && j < len(rb) {
		if unicode.IsDigit(ra[i]) && unicode.IsDigit(rb[j]) {
			si := i
			for i < len(ra) && unicode.IsDigit(ra[i]) {
				i++
			}
			sj := j
			for j < len(rb) && unicode.IsDigit(rb[j]) {
				j++
			}
			na := strings.TrimLeft(string(ra[si:i]), "0")
			nb := strings.TrimLeft(string(rb[sj:j]), "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			continue
		}
		ca, cb := unicode.ToLower(ra[i]), unicode.ToLower(rb[j])
		if ca != cb {
			return ca < cb
		}
		i++
		j++
	}
	return len(ra)-i < len(rb)-j
}
