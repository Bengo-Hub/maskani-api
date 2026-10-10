// Package market serves the public estate showcase (R1) that grows into Maskani Marketplace (R3).
// It reads only properties a tenant chose to publish and never exposes operational data.
package market

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/enquiry"
	"github.com/bengobox/maskani-api/internal/ent/pricelist"
	"github.com/bengobox/maskani-api/internal/ent/property"
	"github.com/bengobox/maskani-api/internal/ent/unit"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
	"github.com/bengobox/maskani-api/internal/shared/page"
	"github.com/bengobox/maskani-api/internal/shared/richtext"
	"github.com/bengobox/maskani-api/internal/shared/secure"
)

// Service is the public market service.
type Service struct {
	client    *ent.Client
	box       *secure.Box
	log       *zap.Logger
	mediaBase string
}

// NewService creates the market service. mediaBase is the API's public origin (MEDIA_URL_BASE),
// so photo keys go out as absolute links the public site can load.
func NewService(client *ent.Client, box *secure.Box, log *zap.Logger, mediaBase string) *Service {
	return &Service{client: client, box: box, log: log.Named("market"), mediaBase: strings.TrimRight(mediaBase, "/")}
}

// PublicMediaKind reports whether a media key holds estate or unit photos, the only uploads served
// without a signed link (they are published marketing images under unguessable names).
func PublicMediaKind(key string) bool {
	parts := strings.SplitN(strings.TrimPrefix(key, "/"), "/", 4)
	return len(parts) == 4 && parts[0] == "tenants" && (parts[2] == "properties" || parts[2] == "units")
}

// photoURLs turns stored photo keys into public links; anything else (another kind, a stray
// value) is left out, so no private key reaches the public site.
func (s *Service) photoURLs(keys []string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if PublicMediaKind(k) && !strings.Contains(k, "..") {
			out = append(out, s.mediaBase+"/media/"+k)
		}
	}
	return out
}

// PublicUnit is the published view of a unit for sale.
type PublicUnit struct {
	ID             uuid.UUID        `json:"id"`
	Code           string           `json:"code"`
	UnitType       string           `json:"unit_type"`
	Bedrooms       *int             `json:"bedrooms,omitempty"`
	Bathrooms      *int             `json:"bathrooms,omitempty"`
	SizeSqm        *decimal.Decimal `json:"size_sqm,omitempty"`
	Floor          string           `json:"floor,omitempty"`
	Status         string           `json:"status"`
	Price          *decimal.Decimal `json:"price,omitempty"`
	ReservationFee *decimal.Decimal `json:"reservation_fee,omitempty"`
	DepositPct     float64          `json:"deposit_pct,omitempty"`
	MaxTermMonths  int              `json:"max_term_months,omitempty"`
	Features       []string         `json:"features,omitempty"`
	Photos         []string         `json:"photos,omitempty"`
}

// PublicEstate is the published view of a property.
type PublicEstate struct {
	ID          uuid.UUID        `json:"id"`
	TenantID    uuid.UUID        `json:"tenant_id"`
	Slug        string           `json:"slug"`
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Area        string           `json:"area,omitempty"`
	Town        string           `json:"town,omitempty"`
	County      string           `json:"county,omitempty"`
	Latitude    *float64         `json:"latitude,omitempty"`
	Longitude   *float64         `json:"longitude,omitempty"`
	Amenities   []string         `json:"amenities,omitempty"`
	Photos      []string         `json:"photos,omitempty"`
	Verified    bool             `json:"verified"`
	Units       []PublicUnit     `json:"units"`
	Available   int              `json:"available"`
	FromPrice   *decimal.Decimal `json:"from_price,omitempty"`
}

// Estates lists published estates (system read: public endpoint, filtered to published only). The
// whole list costs three queries however many estates are published.
func (s *Service) Estates(ctx context.Context) ([]PublicEstate, error) {
	sys := tenantguard.System(ctx)
	props, err := s.client.Property.Query().Where(property.Published(true), property.StatusEQ(property.StatusActive)).
		Order(ent.Asc(property.FieldName)).Limit(100).All(sys)
	if err != nil {
		return nil, err
	}
	return s.estates(sys, props, false)
}

// Estate returns one published estate by its public slug, with units for sale.
func (s *Service) Estate(ctx context.Context, slug string) (*PublicEstate, error) {
	sys := tenantguard.System(ctx)
	p, err := s.client.Property.Query().Where(property.PublicSlug(strings.ToLower(slug)), property.Published(true)).Only(sys)
	if err != nil {
		return nil, err
	}
	out, err := s.estates(sys, []*ent.Property{p}, true)
	if err != nil {
		return nil, err
	}
	return &out[0], nil
}

// prices finds a unit's price item: one set for the unit itself wins over one for its type.
type prices struct {
	byUnit map[uuid.UUID]*ent.PriceListItem
	byType map[string]*ent.PriceListItem
}

func (p prices) find(u *ent.Unit) *ent.PriceListItem {
	if it := p.byUnit[u.ID]; it != nil {
		return it
	}
	return p.byType[u.UnitType]
}

// estates builds the public view of several properties from two queries: every unit for sale
// across them, and every active price list with its items. Listing pages (withUnits false) read
// only available units and the columns the from-price needs.
func (s *Service) estates(ctx context.Context, props []*ent.Property, withUnits bool) ([]PublicEstate, error) {
	ids := make([]uuid.UUID, len(props))
	for i, p := range props {
		ids[i] = p.ID
	}
	uq := s.client.Unit.Query().Where(unit.PropertyIDIn(ids...), unit.StatusEQ(unit.StatusActive))
	if withUnits {
		uq = uq.Where(unit.SaleStatusIn(unit.SaleStatusAvailable, unit.SaleStatusReserved)).Order(ent.Asc(unit.FieldCode)).Limit(1000)
	} else {
		uq = uq.Where(unit.SaleStatusEQ(unit.SaleStatusAvailable)).
			Select(unit.FieldID, unit.FieldPropertyID, unit.FieldUnitType, unit.FieldSaleStatus).Limit(20000)
	}
	units, err := uq.All(ctx)
	if err != nil {
		return nil, err
	}
	lists, err := s.client.PriceList.Query().Where(pricelist.PropertyIDIn(ids...), pricelist.StatusEQ(pricelist.StatusActive)).
		WithItems().All(ctx)
	if err != nil {
		return nil, err
	}
	byProp := map[uuid.UUID]prices{}
	for _, l := range lists {
		pr, ok := byProp[l.PropertyID]
		if !ok {
			pr = prices{byUnit: map[uuid.UUID]*ent.PriceListItem{}, byType: map[string]*ent.PriceListItem{}}
			byProp[l.PropertyID] = pr
		}
		for _, it := range l.Edges.Items {
			if it.UnitID != nil {
				pr.byUnit[*it.UnitID] = it
			} else if _, seen := pr.byType[it.UnitType]; !seen {
				pr.byType[it.UnitType] = it
			}
		}
	}
	out := make([]PublicEstate, len(props))
	index := make(map[uuid.UUID]int, len(props))
	for i, p := range props {
		// The description may be editor HTML; the public site gets readable plain text.
		out[i] = PublicEstate{ID: p.ID, TenantID: p.TenantID, Slug: p.PublicSlug, Name: p.Name,
			Description: richtext.PlainText(p.Description), Area: p.Area, Town: p.Town, County: p.County,
			Latitude: p.Latitude, Longitude: p.Longitude, Amenities: p.Amenities, Photos: s.photoURLs(p.Photos), Verified: true,
			Units: []PublicUnit{}}
		index[p.ID] = i
	}
	for _, u := range units {
		e := &out[index[u.PropertyID]]
		pu := PublicUnit{ID: u.ID, Code: u.Code, UnitType: u.UnitType, Bedrooms: u.Bedrooms, Bathrooms: u.Bathrooms,
			SizeSqm: u.SizeSqm, Floor: u.Floor, Status: string(u.SaleStatus), Features: u.Features, Photos: s.photoURLs(u.Photos)}
		if it := byProp[u.PropertyID].find(u); it != nil {
			price, fee := it.Price, it.ReservationFee
			pu.Price, pu.ReservationFee, pu.DepositPct, pu.MaxTermMonths = &price, &fee, it.DepositPct, it.MaxTermMonths
		}
		if u.SaleStatus == unit.SaleStatusAvailable {
			e.Available++
			if pu.Price != nil && (e.FromPrice == nil || pu.Price.LessThan(*e.FromPrice)) {
				fp := *pu.Price
				e.FromPrice = &fp
			}
		}
		if withUnits {
			e.Units = append(e.Units, pu)
		}
	}
	return out, nil
}

// EnquiryInput is a public enquiry.
type EnquiryInput struct {
	EstateSlug       string     `json:"estate_slug"`
	UnitID           *uuid.UUID `json:"unit_id"`
	Name             string     `json:"name"`
	Phone            string     `json:"phone"`
	Email            string     `json:"email"`
	Message          string     `json:"message"`
	Interest         string     `json:"interest"`
	PreferredContact string     `json:"preferred_contact"`
	Consent          bool       `json:"consent_to_share"`
	Website          string     `json:"website"` // honeypot: bots fill it, people never see it
}

// Enquire records an enquiry against a published estate.
func (s *Service) Enquire(ctx context.Context, ip string, in EnquiryInput) error {
	if in.Website != "" {
		return nil
	}
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 120 || len(in.Message) > 2000 {
		return httpx.Invalid("please give your name and a short message")
	}
	phone := secure.NormalizePhone(in.Phone)
	if phone == "" && in.Email == "" {
		return httpx.Invalid("a phone number or email is required")
	}
	if !in.Consent {
		return httpx.Invalid("please agree to be contacted")
	}
	sys := tenantguard.System(ctx)
	p, err := s.client.Property.Query().Where(property.PublicSlug(strings.ToLower(in.EstateSlug)), property.Published(true)).Only(sys)
	if err != nil {
		return httpx.Invalid("estate not found")
	}
	tctx := tenantguard.With(ctx, p.TenantID)
	enc, _ := s.box.Encrypt(phone)
	interest := enquiry.Interest(in.Interest)
	if in.Interest == "" {
		interest = enquiry.InterestBuy
	}
	pc := enquiry.PreferredContact(in.PreferredContact)
	if in.PreferredContact == "" {
		pc = enquiry.PreferredContactCall
	}
	c := s.client.Enquiry.Create().SetPropertyID(p.ID).SetName(strings.TrimSpace(in.Name)).SetPhoneEnc(enc).
		SetPhoneHash(s.box.Hash(phone)).SetEmail(strings.TrimSpace(in.Email)).SetMessage(in.Message).
		SetInterest(interest).SetPreferredContact(pc).SetConsentToShare(true).SetIPHash(s.box.Hash("ip:" + ip))
	if in.UnitID != nil {
		c.SetUnitID(*in.UnitID)
	}
	return c.Exec(tctx)
}

// EnquiryView decrypts the phone for staff.
type EnquiryView struct {
	*ent.Enquiry
	Phone string `json:"phone,omitempty"`
}

// ListEnquiries returns a keyset page of enquiries for staff (tenant scoped, newest first), for one
// property or the caller's properties.
func (s *Service) ListEnquiries(ctx context.Context, propertyID *uuid.UUID, scope []uuid.UUID, all bool, status string, p page.Params) (page.Result[EnquiryView], error) {
	q := s.client.Enquiry.Query()
	if propertyID != nil {
		q = q.Where(enquiry.PropertyID(*propertyID))
	} else if !all {
		q = q.Where(enquiry.PropertyIDIn(scope...))
	}
	if status != "" {
		q = q.Where(enquiry.StatusEQ(enquiry.Status(status)))
	}
	rows, err := q.Where(p.Predicate()).Modify(page.Order()).Limit(p.Limit + 1).All(ctx)
	if err != nil {
		return page.Result[EnquiryView]{}, err
	}
	res := page.Build(rows, p.Limit, func(e *ent.Enquiry) (uuid.UUID, time.Time) { return e.ID, e.CreatedAt })
	out := make([]EnquiryView, len(res.Data))
	for i, r := range res.Data {
		out[i] = EnquiryView{Enquiry: r}
		if r.ConsentToShare {
			out[i].Phone, _ = s.box.Decrypt(r.PhoneEnc)
		}
	}
	return page.Result[EnquiryView]{Data: out, NextCursor: res.NextCursor, HasMore: res.HasMore}, nil
}

// UpdateEnquiry changes an enquiry's status.
func (s *Service) UpdateEnquiry(ctx context.Context, id uuid.UUID, status string, assignee *uuid.UUID) error {
	u := s.client.Enquiry.UpdateOneID(id).SetStatus(enquiry.Status(status))
	if assignee != nil {
		u.SetAssignedTo(*assignee)
	}
	return u.Exec(ctx)
}
