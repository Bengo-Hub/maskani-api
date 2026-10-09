package register

import (
	"context"
	"strings"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/unit"
	"github.com/bengobox/maskani-api/internal/ent/unitaccount"
	"github.com/bengobox/maskani-api/internal/ent/unitparty"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/shared/page"
)

// UnitInput is the create and update body.
type UnitInput struct {
	PropertyID      *uuid.UUID     `json:"property_id"`
	BlockID         *uuid.UUID     `json:"block_id"`
	Code            *string        `json:"code"`
	UnitType        *string        `json:"unit_type"`
	Use             *string        `json:"use"`
	Bedrooms        *int           `json:"bedrooms"`
	Bathrooms       *int           `json:"bathrooms"`
	SizeSqm         *float64       `json:"size_sqm"`
	PlotSizeSqm     *float64       `json:"plot_size_sqm"`
	Floor           *string        `json:"floor"`
	Entitlement     *float64       `json:"entitlement"`
	ParkingBays     *int           `json:"parking_bays"`
	Furnished       *bool          `json:"furnished"`
	Phase           *string        `json:"phase"`
	SaleStatus      *string        `json:"sale_status"`
	OccupancyStatus *string        `json:"occupancy_status"`
	Rentable        *bool          `json:"rentable"`
	Features        []string       `json:"features"`
	WalkingOrder    *int           `json:"walking_order"`
	Status          *string        `json:"status"`
	CustomFields    map[string]any `json:"custom_fields"`
}

func dec(f *float64) decimal.Decimal { return decimal.NewFromFloat(*f) }

// CreateUnit adds a unit. Codes are unique per property and upper-cased.
func (s *Service) CreateUnit(ctx context.Context, actor uuid.UUID, in UnitInput) (*ent.Unit, error) {
	if in.PropertyID == nil || in.Code == nil || strings.TrimSpace(*in.Code) == "" {
		return nil, httpx.Invalid("property_id and code are required")
	}
	c := s.client.Unit.Create().SetPropertyID(*in.PropertyID).
		SetCode(strings.ToUpper(strings.TrimSpace(*in.Code))).SetCreatedBy(actor)
	if in.BlockID != nil {
		c.SetBlockID(*in.BlockID)
	}
	if in.UnitType != nil {
		c.SetUnitType(*in.UnitType)
	}
	if in.Use != nil {
		c.SetUse(unit.Use(*in.Use))
	}
	if in.Bedrooms != nil {
		c.SetBedrooms(*in.Bedrooms)
	}
	if in.Bathrooms != nil {
		c.SetBathrooms(*in.Bathrooms)
	}
	if in.SizeSqm != nil {
		c.SetSizeSqm(dec(in.SizeSqm))
	}
	if in.PlotSizeSqm != nil {
		c.SetPlotSizeSqm(dec(in.PlotSizeSqm))
	}
	if in.Floor != nil {
		c.SetFloor(*in.Floor)
	}
	if in.Entitlement != nil {
		c.SetEntitlement(dec(in.Entitlement))
	}
	if in.ParkingBays != nil {
		c.SetParkingBays(*in.ParkingBays)
	}
	if in.Furnished != nil {
		c.SetFurnished(*in.Furnished)
	}
	if in.Phase != nil {
		c.SetPhase(*in.Phase)
	}
	if in.SaleStatus != nil {
		c.SetSaleStatus(unit.SaleStatus(*in.SaleStatus))
	}
	if in.OccupancyStatus != nil {
		c.SetOccupancyStatus(unit.OccupancyStatus(*in.OccupancyStatus))
	}
	if in.Rentable != nil {
		c.SetRentable(*in.Rentable)
	}
	if in.Features != nil {
		c.SetFeatures(in.Features)
	}
	if in.WalkingOrder != nil {
		c.SetWalkingOrder(*in.WalkingOrder)
	}
	if in.CustomFields != nil {
		c.SetCustomFields(in.CustomFields)
	}
	return c.Save(ctx)
}

// UpdateUnit applies a partial update.
func (s *Service) UpdateUnit(ctx context.Context, id uuid.UUID, in UnitInput) (*ent.Unit, error) {
	u := s.client.Unit.UpdateOneID(id)
	if in.BlockID != nil {
		u.SetBlockID(*in.BlockID)
	}
	if in.UnitType != nil {
		u.SetUnitType(*in.UnitType)
	}
	if in.Use != nil {
		u.SetUse(unit.Use(*in.Use))
	}
	if in.Bedrooms != nil {
		u.SetBedrooms(*in.Bedrooms)
	}
	if in.Bathrooms != nil {
		u.SetBathrooms(*in.Bathrooms)
	}
	if in.SizeSqm != nil {
		u.SetSizeSqm(dec(in.SizeSqm))
	}
	if in.PlotSizeSqm != nil {
		u.SetPlotSizeSqm(dec(in.PlotSizeSqm))
	}
	if in.Floor != nil {
		u.SetFloor(*in.Floor)
	}
	if in.Entitlement != nil {
		u.SetEntitlement(dec(in.Entitlement))
	}
	if in.ParkingBays != nil {
		u.SetParkingBays(*in.ParkingBays)
	}
	if in.Furnished != nil {
		u.SetFurnished(*in.Furnished)
	}
	if in.Phase != nil {
		u.SetPhase(*in.Phase)
	}
	if in.SaleStatus != nil {
		u.SetSaleStatus(unit.SaleStatus(*in.SaleStatus))
	}
	if in.OccupancyStatus != nil {
		u.SetOccupancyStatus(unit.OccupancyStatus(*in.OccupancyStatus))
	}
	if in.Rentable != nil {
		u.SetRentable(*in.Rentable)
	}
	if in.Features != nil {
		u.SetFeatures(in.Features)
	}
	if in.WalkingOrder != nil {
		u.SetWalkingOrder(*in.WalkingOrder)
	}
	if in.Status != nil {
		u.SetStatus(unit.Status(*in.Status))
	}
	if in.CustomFields != nil {
		u.SetCustomFields(in.CustomFields)
	}
	return u.Save(ctx)
}

// UnitFilter narrows a unit list.
type UnitFilter struct {
	PropertyID      *uuid.UUID
	BlockID         *uuid.UUID
	SaleStatus      string
	OccupancyStatus string
	Q               string
	Scope           []uuid.UUID
	AllProperties   bool
}

// UnitRow is a unit with its primary owner and account balances for list screens.
type UnitRow struct {
	*ent.Unit
	OwnerName string          `json:"owner_name,omitempty"`
	Balance   decimal.Decimal `json:"balance"`
}

// ListUnits returns a keyset page of units with owner names and estate balances, loaded in two
// bounded follow-up queries keyed by the page's unit ids (no N+1).
func (s *Service) ListUnits(ctx context.Context, f UnitFilter, p page.Params) (page.Result[UnitRow], error) {
	q := s.client.Unit.Query().Where(unit.StatusEQ(unit.StatusActive))
	if f.PropertyID != nil {
		q = q.Where(unit.PropertyID(*f.PropertyID))
	}
	if !f.AllProperties {
		q = q.Where(unit.PropertyIDIn(f.Scope...))
	}
	if f.BlockID != nil {
		q = q.Where(unit.BlockID(*f.BlockID))
	}
	if f.SaleStatus != "" {
		q = q.Where(unit.SaleStatusEQ(unit.SaleStatus(f.SaleStatus)))
	}
	if f.OccupancyStatus != "" {
		q = q.Where(unit.OccupancyStatusEQ(unit.OccupancyStatus(f.OccupancyStatus)))
	}
	if t := strings.TrimSpace(f.Q); t != "" {
		q = q.Where(func(s *sql.Selector) { s.Where(sql.HasPrefix(s.C(unit.FieldCode), strings.ToUpper(t))) })
	}
	units, err := q.Where(p.Predicate()).Modify(page.Order()).Limit(p.Limit + 1).All(ctx)
	if err != nil {
		return page.Result[UnitRow]{}, err
	}
	res := page.Build(units, p.Limit, func(u *ent.Unit) (uuid.UUID, time.Time) { return u.ID, u.CreatedAt })
	rows := make([]UnitRow, len(res.Data))
	ids := make([]uuid.UUID, len(res.Data))
	for i, u := range res.Data {
		rows[i] = UnitRow{Unit: u}
		ids[i] = u.ID
	}
	if len(ids) > 0 {
		owners, _ := s.client.UnitParty.Query().
			Where(unitparty.UnitIDIn(ids...), unitparty.RoleIn(unitparty.RoleOwner, unitparty.RoleBuyer),
				unitparty.StatusEQ(unitparty.StatusActive)).WithParty().All(ctx)
		accs, _ := s.client.UnitAccount.Query().Where(unitaccount.UnitIDIn(ids...)).All(ctx)
		idx := map[uuid.UUID]int{}
		for i, id := range ids {
			idx[id] = i
		}
		for _, o := range owners {
			if i, ok := idx[o.UnitID]; ok && o.Edges.Party != nil && rows[i].OwnerName == "" {
				rows[i].OwnerName = o.Edges.Party.DisplayName
			}
		}
		for _, a := range accs {
			if i, ok := idx[a.UnitID]; ok {
				rows[i].Balance = rows[i].Balance.Add(a.Balance)
			}
		}
	}
	return page.Result[UnitRow]{Data: rows, NextCursor: res.NextCursor, HasMore: res.HasMore}, nil
}

// UnitCode is a compact unit reference for pickers.
type UnitCode struct {
	ID   uuid.UUID `json:"id"`
	Code string    `json:"code"`
}

// ListUnitCodes returns every active unit code of a property (pickers, gate host lookup).
func (s *Service) ListUnitCodes(ctx context.Context, propertyID uuid.UUID) ([]UnitCode, error) {
	rows, err := s.client.Unit.Query().Where(unit.PropertyID(propertyID), unit.StatusEQ(unit.StatusActive)).
		Order(ent.Asc(unit.FieldCode)).Select(unit.FieldID, unit.FieldCode).Limit(5000).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]UnitCode, len(rows))
	for i, u := range rows {
		out[i] = UnitCode{ID: u.ID, Code: u.Code}
	}
	return out, nil
}

// UnitDetail is the unit screen payload.
type UnitDetail struct {
	*ent.Unit
	Parties  []*ent.UnitParty   `json:"parties"`
	Accounts []*ent.UnitAccount `json:"accounts"`
}

// GetUnit returns a unit with its parties and accounts.
func (s *Service) GetUnit(ctx context.Context, id uuid.UUID) (*UnitDetail, error) {
	u, err := s.client.Unit.Query().Where(unit.ID(id)).WithBlock().Only(ctx)
	if err != nil {
		return nil, err
	}
	parties, err := s.client.UnitParty.Query().Where(unitparty.UnitID(id)).WithParty().
		Order(ent.Desc(unitparty.FieldStartDate)).All(ctx)
	if err != nil {
		return nil, err
	}
	accs, err := s.client.UnitAccount.Query().Where(unitaccount.UnitID(id)).WithFund().All(ctx)
	if err != nil {
		return nil, err
	}
	return &UnitDetail{Unit: u, Parties: parties, Accounts: accs}, nil
}
