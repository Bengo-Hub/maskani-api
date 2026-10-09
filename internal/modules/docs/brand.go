// Package docs builds Maskani's downloadable documents (statements, reports) on the shared report
// engine github.com/Bengo-Hub/reports: this package maps Maskani data and tenant branding into a
// reports.Report; the engine renders PDF, CSV and Excel.
package docs

import (
	"context"
	"strings"
	"time"

	sharedcache "github.com/Bengo-Hub/cache"
	"github.com/Bengo-Hub/reports"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/modules/settings"
)

// logoTTL keeps a tenant logo in memory so repeated downloads do not fetch it each time.
const logoTTL = 30 * time.Minute

type logo struct {
	bytes []byte
	kind  string
}

// Brander stamps tenant branding onto reports: name, colour, logo and contact lines from auth-api's
// tenant record (through the shared Redis tenant cache), and the provider footer setting.
type Brander struct {
	aside    *sharedcache.Aside
	authURL  string
	settings *settings.Service
	logos    *sharedcache.Local[string, logo]
	log      *zap.Logger
}

// NewBrander creates the brander. authURL is auth-api's in-cluster base URL.
func NewBrander(aside *sharedcache.Aside, authURL string, st *settings.Service, log *zap.Logger) *Brander {
	return &Brander{aside: aside, authURL: authURL, settings: st,
		logos: sharedcache.NewLocal[string, logo](500, logoTTL), log: log.Named("docs")}
}

// Apply fills the report's branding. A missing tenant record or logo never fails a download: the
// report renders with the slug as the name and no logo.
func (b *Brander) Apply(ctx context.Context, tenantID uuid.UUID, slug string, r *reports.Report) {
	r.TenantName = slug
	r.ProviderFooterEnabled = b.providerFooter(ctx, tenantID)
	td, err := sharedcache.GetTenantDetails(ctx, b.aside, b.authURL, slug, 0)
	if err != nil {
		b.log.Warn("tenant details unavailable for branding", zap.String("tenant", slug), zap.Error(err))
		return
	}
	brand := sharedcache.GetTenantBranding(td)
	if brand.Name != "" {
		r.TenantName = brand.Name
	}
	r.PrimaryColor = brand.PrimaryColor
	r.Address = address(td.Metadata, td.Country)
	if brand.Phone != "" {
		r.Meta = append(r.Meta, [2]string{"Phone", brand.Phone})
	}
	if brand.Email != "" {
		r.Meta = append(r.Meta, [2]string{"Email", brand.Email})
	}
	if td.LogoURL != "" {
		l, ok := b.logos.Get(td.LogoURL)
		if !ok {
			data, kind := sharedcache.FetchLogo(td.LogoURL)
			l = logo{bytes: data, kind: kind}
			b.logos.Set(td.LogoURL, l) // a failed fetch is cached too, so a dead logo URL is not retried per download
		}
		r.LogoPNG, r.LogoType = l.bytes, l.kind
	}
}

// providerFooter: on unless the tenant's settings metadata sets provider_footer_enabled to false.
func (b *Brander) providerFooter(ctx context.Context, tenantID uuid.UUID) bool {
	st, err := b.settings.Get(ctx, tenantID)
	if err != nil || st == nil {
		return true
	}
	if v, ok := st.Metadata["provider_footer_enabled"].(bool); ok {
		return v
	}
	return true
}

// address joins the tenant's address lines from auth-api metadata, falling back to the country.
func address(meta map[string]any, country string) string {
	str := func(k string) string {
		v, _ := meta[k].(string)
		return strings.TrimSpace(v)
	}
	var lines []string
	if a := str("address"); a != "" {
		lines = append(lines, a)
	}
	city := strings.TrimSpace(strings.Join(nonEmpty(str("postal_code"), str("city")), " "))
	if city != "" {
		lines = append(lines, city)
	}
	if c := str("country_name"); c != "" {
		lines = append(lines, c)
	} else if len(lines) == 0 && country != "" {
		lines = append(lines, country)
	}
	return strings.Join(lines, "\n")
}

func nonEmpty(v ...string) []string {
	out := v[:0]
	for _, s := range v {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
