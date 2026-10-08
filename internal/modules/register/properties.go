// Package register owns properties, blocks, units, parties and their dated relationships.
package register

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/block"
	"github.com/bengobox/maskani-api/internal/ent/property"
	"github.com/bengobox/maskani-api/internal/ent/unit"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/accounts"
	"github.com/bengobox/maskani-api/internal/modules/authapi"
	"github.com/bengobox/maskani-api/internal/shared/richtext"
	"github.com/bengobox/maskani-api/internal/shared/secure"
	"github.com/bengobox/maskani-api/internal/shared/sqlx"
)

// Service is the register domain service.
type Service struct {
	client   *ent.Client
	auth     *authapi.Client
	accounts *accounts.Service
	box      *secure.Box
	log      *zap.Logger
}

// NewService creates the register service.
func NewService(client *ent.Client, auth *authapi.Client, acc *accounts.Service, box *secure.Box, log *zap.Logger) *Service {
	return &Service{client: client, auth: auth, accounts: acc, box: box, log: log.Named("register")}
}

// PropertyInput is the create and update body.
type PropertyInput struct {
	Code            *string         `json:"code"`
	Name            *string         `json:"name"`
	PropertyType    *string         `json:"property_type"`
	UseCase         *string         `json:"use_case"`
	Description     *string         `json:"description"`
	AddressLine     *string         `json:"address_line"`
	Area            *string         `json:"area"`
	Town            *string         `json:"town"`
	County          *string         `json:"county"`
	Latitude        *float64        `json:"latitude"`
	Longitude       *float64        `json:"longitude"`
	PlotNumber      *string         `json:"plot_number"`
	TitleNumber     *string         `json:"title_number"`
	YearBuilt       *int            `json:"year_built"`
	Phases          []string        `json:"phases"`
	Amenities       []string        `json:"amenities"`
	ModuleOverrides map[string]bool `json:"module_overrides"`
	Published       *bool           `json:"published"`
	PublicSlug      *string         `json:"public_slug"`
	Status          *string         `json:"status"`
	CustomFields    map[string]any  `json:"custom_fields"`
}

// CreateProperty registers the property and its auth-api outlet. The outlet is created with the
// caller's bearer token because auth-api requires a tenant admin; if that fails the property is
// still created and can be linked later (outlet_id stays empty).
func (s *Service) CreateProperty(ctx context.Context, tenantSlug, bearer string, actor uuid.UUID, in PropertyInput) (*ent.Property, error) {
	if in.Name == nil || strings.TrimSpace(*in.Name) == "" {
		return nil, httpx.Invalid("name is required")
	}
	code := ""
	if in.Code != nil {
		code = strings.ToUpper(strings.TrimSpace(*in.Code))
	}
	if code == "" {
		code = codeFromName(*in.Name)
	}
	c := s.client.Property.Create().SetCode(code).SetName(strings.TrimSpace(*in.Name)).SetCreatedBy(actor)
	applyPropertyCreate(c, in)
	if bearer != "" && s.auth != nil {
		addr := ""
		if in.AddressLine != nil {
			addr = *in.AddressLine
		}
		if o, err := s.auth.CreateOutlet(ctx, tenantSlug, bearer, authapi.OutletRequest{
			Code: code, Name: *in.Name, UseCase: "property", Address: addr,
		}); err == nil {
			c.SetOutletID(o.ID)
		} else {
			s.log.Warn("outlet creation failed; property created without outlet", zap.Error(err))
		}
	}
	return c.Save(ctx)
}

func applyPropertyCreate(c *ent.PropertyCreate, in PropertyInput) {
	if in.PropertyType != nil {
		c.SetPropertyType(*in.PropertyType)
	}
	if in.UseCase != nil {
		c.SetUseCase(*in.UseCase)
	}
	if in.Description != nil {
		c.SetDescription(richtext.Sanitize(*in.Description))
	}
	if in.AddressLine != nil {
		c.SetAddressLine(*in.AddressLine)
	}
	if in.Area != nil {
		c.SetArea(*in.Area)
	}
	if in.Town != nil {
		c.SetTown(*in.Town)
	}
	if in.County != nil {
		c.SetCounty(*in.County)
	}
	if in.Latitude != nil {
		c.SetLatitude(*in.Latitude)
	}
	if in.Longitude != nil {
		c.SetLongitude(*in.Longitude)
	}
	if in.PlotNumber != nil {
		c.SetPlotNumber(*in.PlotNumber)
	}
	if in.TitleNumber != nil {
		c.SetTitleNumber(*in.TitleNumber)
	}
	if in.YearBuilt != nil {
		c.SetYearBuilt(*in.YearBuilt)
	}
	if in.Phases != nil {
		c.SetPhases(in.Phases)
	}
	if in.Amenities != nil {
		c.SetAmenities(in.Amenities)
	}
	if in.ModuleOverrides != nil {
		c.SetModuleOverrides(in.ModuleOverrides)
	}
	if in.Published != nil {
		c.SetPublished(*in.Published)
	}
	if in.PublicSlug != nil {
		c.SetPublicSlug(*in.PublicSlug)
	}
	if in.CustomFields != nil {
		c.SetCustomFields(in.CustomFields)
	}
}

// UpdateProperty applies a partial update.
func (s *Service) UpdateProperty(ctx context.Context, id uuid.UUID, in PropertyInput) (*ent.Property, error) {
	u := s.client.Property.UpdateOneID(id)
	if in.Name != nil {
		u.SetName(strings.TrimSpace(*in.Name))
	}
	if in.PropertyType != nil {
		u.SetPropertyType(*in.PropertyType)
	}
	if in.UseCase != nil {
		u.SetUseCase(*in.UseCase)
	}
	if in.Description != nil {
		u.SetDescription(richtext.Sanitize(*in.Description))
	}
	if in.AddressLine != nil {
		u.SetAddressLine(*in.AddressLine)
	}
	if in.Area != nil {
		u.SetArea(*in.Area)
	}
	if in.Town != nil {
		u.SetTown(*in.Town)
	}
	if in.County != nil {
		u.SetCounty(*in.County)
	}
	if in.Latitude != nil {
		u.SetLatitude(*in.Latitude)
	}
	if in.Longitude != nil {
		u.SetLongitude(*in.Longitude)
	}
	if in.PlotNumber != nil {
		u.SetPlotNumber(*in.PlotNumber)
	}
	if in.TitleNumber != nil {
		u.SetTitleNumber(*in.TitleNumber)
	}
	if in.YearBuilt != nil {
		u.SetYearBuilt(*in.YearBuilt)
	}
	if in.Phases != nil {
		u.SetPhases(in.Phases)
	}
	if in.Amenities != nil {
		u.SetAmenities(in.Amenities)
	}
	if in.ModuleOverrides != nil {
		u.SetModuleOverrides(in.ModuleOverrides)
	}
	if in.Published != nil {
		u.SetPublished(*in.Published)
	}
	if in.PublicSlug != nil {
		u.SetPublicSlug(*in.PublicSlug)
	}
	if in.Status != nil {
		u.SetStatus(property.Status(*in.Status))
	}
	if in.CustomFields != nil {
		u.SetCustomFields(in.CustomFields)
	}
	return u.Save(ctx)
}

// PropertySummary is a property with unit counts for list screens.
type PropertySummary struct {
	*ent.Property
	UnitCount     int `json:"unit_count"`
	OccupiedCount int `json:"occupied_count"`
	SoldCount     int `json:"sold_count"`
}

// ListProperties returns visible properties with unit counts computed in one grouped SQL query.
func (s *Service) ListProperties(ctx context.Context, scope []uuid.UUID, all bool, status string) ([]PropertySummary, error) {
	q := s.client.Property.Query()
	if !all {
		q = q.Where(property.IDIn(scope...))
	}
	if status != "" {
		q = q.Where(property.StatusEQ(property.Status(status)))
	} else {
		q = q.Where(property.StatusEQ(property.StatusActive))
	}
	props, err := q.Order(ent.Asc(property.FieldName)).Limit(500).All(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, len(props))
	for i, p := range props {
		ids[i] = p.ID
	}
	var counts []struct {
		PropertyID uuid.UUID `json:"property_id"`
		Total      int       `json:"total"`
		Occupied   int       `json:"occupied"`
		Sold       int       `json:"sold"`
	}
	if len(ids) > 0 {
		err = s.client.Unit.Query().
			Where(unit.PropertyIDIn(ids...), unit.StatusEQ(unit.StatusActive)).
			GroupBy(unit.FieldPropertyID).
			Aggregate(sqlx.CountAs("total", ""), sqlx.CountAs("occupied", "occupancy_status IN ('owner_occupied','tenanted')"),
				sqlx.CountAs("sold", "sale_status IN ('under_agreement','fully_paid','handed_over','titled','in_default')")).
			Scan(ctx, &counts)
		if err != nil {
			return nil, err
		}
	}
	byID := map[uuid.UUID]int{}
	for i, c := range counts {
		byID[c.PropertyID] = i
	}
	out := make([]PropertySummary, len(props))
	for i, p := range props {
		out[i] = PropertySummary{Property: p}
		if j, ok := byID[p.ID]; ok {
			out[i].UnitCount, out[i].OccupiedCount, out[i].SoldCount = counts[j].Total, counts[j].Occupied, counts[j].Sold
		}
	}
	return out, nil
}

// GetProperty returns a property with its blocks.
func (s *Service) GetProperty(ctx context.Context, id uuid.UUID) (*ent.Property, error) {
	return s.client.Property.Query().Where(property.ID(id)).
		WithBlocks(func(q *ent.BlockQuery) { q.Order(ent.Asc(block.FieldSort), ent.Asc(block.FieldCode)) }).Only(ctx)
}

// BlockInput creates or updates a block.
type BlockInput struct {
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	Phase       *string `json:"phase"`
	Floors      *int    `json:"floors"`
	Sort        *int    `json:"sort"`
	Description *string `json:"description"`
}

// CreateBlock adds a block to a property.
func (s *Service) CreateBlock(ctx context.Context, propertyID uuid.UUID, in BlockInput) (*ent.Block, error) {
	if strings.TrimSpace(in.Code) == "" {
		return nil, httpx.Invalid("code is required")
	}
	name := in.Name
	if name == "" {
		name = "Block " + in.Code
	}
	c := s.client.Block.Create().SetPropertyID(propertyID).SetCode(strings.ToUpper(strings.TrimSpace(in.Code))).SetName(name)
	if in.Phase != nil {
		c.SetPhase(*in.Phase)
	}
	if in.Floors != nil {
		c.SetFloors(*in.Floors)
	}
	if in.Sort != nil {
		c.SetSort(*in.Sort)
	}
	if in.Description != nil {
		c.SetDescription(richtext.Sanitize(*in.Description))
	}
	return c.Save(ctx)
}

func codeFromName(name string) string {
	var b strings.Builder
	for _, w := range strings.Fields(strings.ToUpper(name)) {
		if len(w) > 0 {
			b.WriteByte(w[0])
		}
	}
	if b.Len() < 2 {
		compact := strings.ToUpper(strings.ReplaceAll(name, " ", ""))
		return fmt.Sprintf("P%s", compact[:min(4, len(compact))])
	}
	return b.String()
}
