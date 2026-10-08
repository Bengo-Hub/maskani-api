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
	"github.com/bengobox/maskani-api/internal/ent/pricelistitem"
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
	client *ent.Client
	box    *secure.Box
	log    *zap.Logger
}

// NewService creates the market service.
func NewService(client *ent.Client, box *secure.Box, log *zap.Logger) *Service {
	return &Service{client: client, box: box, log: log.Named("market")}
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

// Estates lists published estates (system read: public endpoint, filtered to published only).
func (s *Service) Estates(ctx context.Context) ([]PublicEstate, error) {
	sys := tenantguard.System(ctx)
	props, err := s.client.Property.Query().Where(property.Published(true), property.StatusEQ(property.StatusActive)).
		Order(ent.Asc(property.FieldName)).Limit(100).All(sys)
	if err != nil {
		return nil, err
	}
	out := make([]PublicEstate, 0, len(props))
	for _, p := range props {
		e, err := s.estate(sys, p, false)
		if err == nil {
			out = append(out, *e)
		}
	}
	return out, nil
}

// Estate returns one published estate by its public slug, with units for sale.
func (s *Service) Estate(ctx context.Context, slug string) (*PublicEstate, error) {
	sys := tenantguard.System(ctx)
	p, err := s.client.Property.Query().Where(property.PublicSlug(strings.ToLower(slug)), property.Published(true)).Only(sys)
	if err != nil {
		return nil, err
	}
	return s.estate(sys, p, true)
}

func (s *Service) estate(ctx context.Context, p *ent.Property, withUnits bool) (*PublicEstate, error) {
	// The description may be editor HTML; the public site gets readable plain text.
	e := &PublicEstate{ID: p.ID, TenantID: p.TenantID, Slug: p.PublicSlug, Name: p.Name, Description: richtext.PlainText(p.Description),
		Area: p.Area, Town: p.Town, County: p.County, Latitude: p.Latitude, Longitude: p.Longitude,
		Amenities: p.Amenities, Photos: p.Photos, Verified: true}
	units, err := s.client.Unit.Query().Where(unit.TenantID(p.TenantID), unit.PropertyID(p.ID), unit.StatusEQ(unit.StatusActive),
		unit.SaleStatusIn(unit.SaleStatusAvailable, unit.SaleStatusReserved)).Order(ent.Asc(unit.FieldCode)).Limit(1000).All(ctx)
	if err != nil {
		return nil, err
	}
	items, err := s.client.PriceListItem.Query().Where(pricelistitem.TenantID(p.TenantID),
		pricelistitem.HasPriceListWith(pricelist.PropertyID(p.ID), pricelist.StatusEQ(pricelist.StatusActive))).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, u := range units {
		pu := PublicUnit{ID: u.ID, Code: u.Code, UnitType: u.UnitType, Bedrooms: u.Bedrooms, Bathrooms: u.Bathrooms,
			SizeSqm: u.SizeSqm, Floor: u.Floor, Status: string(u.SaleStatus), Features: u.Features, Photos: u.Photos}
		for _, it := range items {
			if (it.UnitID != nil && *it.UnitID == u.ID) || (it.UnitID == nil && it.UnitType == u.UnitType && pu.Price == nil) {
				price, fee := it.Price, it.ReservationFee
				pu.Price, pu.ReservationFee, pu.DepositPct, pu.MaxTermMonths = &price, &fee, it.DepositPct, it.MaxTermMonths
			}
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
	if e.Units == nil {
		e.Units = []PublicUnit{}
	}
	return e, nil
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
