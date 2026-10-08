// Command seed-tenant loads the Shaba Village demo dataset into one tenant. It is idempotent: every
// record is looked up by its natural key first (property code, block code, unit code, party phone,
// charge code, meter serial, vendor name, price list name, badge, title), so a second run creates
// nothing new. Writes go through the same domain services the API uses.
//
//	seed-tenant --tenant codevertex-demo [--dry-run] [--property-code SHABA]
//
// Inside the cluster: kubectl exec deploy/maskani-api -- /app/seed-tenant --tenant codevertex-demo
//
// --dry-run prints what would be created and writes nothing; it never calls treasury, auth-api or
// notifications. A real run calls treasury where the API would (paybill routes, reservation and
// instalment invoices) and auth-api only when SEED_BEARER_TOKEN holds a tenant admin token (to
// create the property outlet). Any of those failing is logged and the run continues; the API's
// background jobs retry route registration, and a later run fills any gap.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/config"
	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/block"
	"github.com/bengobox/maskani-api/internal/ent/chargerate"
	"github.com/bengobox/maskani-api/internal/ent/chargetype"
	"github.com/bengobox/maskani-api/internal/ent/fund"
	"github.com/bengobox/maskani-api/internal/ent/meter"
	"github.com/bengobox/maskani-api/internal/ent/meterreading"
	"github.com/bengobox/maskani-api/internal/ent/notice"
	"github.com/bengobox/maskani-api/internal/ent/outlet"
	"github.com/bengobox/maskani-api/internal/ent/party"
	"github.com/bengobox/maskani-api/internal/ent/pricelist"
	"github.com/bengobox/maskani-api/internal/ent/property"
	"github.com/bengobox/maskani-api/internal/ent/reservation"
	_ "github.com/bengobox/maskani-api/internal/ent/runtime"
	"github.com/bengobox/maskani-api/internal/ent/salecontract"
	enttenant "github.com/bengobox/maskani-api/internal/ent/tenant"
	"github.com/bengobox/maskani-api/internal/ent/unit"
	"github.com/bengobox/maskani-api/internal/ent/unitparty"
	"github.com/bengobox/maskani-api/internal/ent/vendor"
	"github.com/bengobox/maskani-api/internal/ent/vendordocument"
	"github.com/bengobox/maskani-api/internal/ent/vendorpersonnel"
	"github.com/bengobox/maskani-api/internal/ent/visitorpass"
	"github.com/bengobox/maskani-api/internal/ent/workorder"
	"github.com/bengobox/maskani-api/internal/modules/accounts"
	"github.com/bengobox/maskani-api/internal/modules/authapi"
	"github.com/bengobox/maskani-api/internal/modules/billing"
	"github.com/bengobox/maskani-api/internal/modules/gate"
	"github.com/bengobox/maskani-api/internal/modules/notices"
	"github.com/bengobox/maskani-api/internal/modules/notify"
	"github.com/bengobox/maskani-api/internal/modules/register"
	"github.com/bengobox/maskani-api/internal/modules/sales"
	"github.com/bengobox/maskani-api/internal/modules/sequence"
	"github.com/bengobox/maskani-api/internal/modules/settings"
	"github.com/bengobox/maskani-api/internal/modules/tenant"
	"github.com/bengobox/maskani-api/internal/modules/treasury"
	"github.com/bengobox/maskani-api/internal/modules/utilities"
	"github.com/bengobox/maskani-api/internal/modules/works"
	"github.com/bengobox/maskani-api/internal/platform/database"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
	"github.com/bengobox/maskani-api/internal/shared/secure"
)

// rateFrom is when the demo tariff starts.
var rateFrom = time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("EAT", 3*3600))

type seeder struct {
	dry      bool
	ctx      context.Context
	client   *ent.Client
	box      *secure.Box
	loc      *time.Location
	log      *zap.Logger
	tenantID uuid.UUID
	slug     string
	bearer   string
	// resident is a real person who takes over the B07 owner so they can sign in to the owner portal
	// (set from SEED_DEMO_RESIDENT_* at run time, never committed). Empty phone means none.
	resident  demoResident
	portalURL string

	settings *settings.Service
	register *register.Service
	billing  *billing.Service
	util     *utilities.Service
	sales    *sales.Service
	works    *works.Service
	gate     *gate.Service
	notices  *notices.Service

	created, existing int
}

func main() {
	tenantArg := flag.String("tenant", "", "tenant slug or UUID (required)")
	dry := flag.Bool("dry-run", false, "print what would be created and write nothing")
	code := flag.String("property-code", "SHABA", "property code of the demo estate")
	keepEvents := flag.Bool("keep-events", false, "deliver the outbox events the seed raises (default: mark them delivered so no messages go to demo numbers)")
	flag.Parse()
	if strings.TrimSpace(*tenantArg) == "" {
		flag.Usage()
		os.Exit(2)
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	zl, _ := zap.NewDevelopment()
	defer zl.Sync() //nolint:errcheck
	loc, err := time.LoadLocation(cfg.App.Timezone)
	if err != nil {
		loc = time.FixedZone("EAT", 3*3600)
	}
	db, err := database.OpenSQL(cfg.Postgres.URL, config.PostgresConfig{MaxOpenConns: 4, MaxIdleConns: 2, ConnMaxLifetime: 5 * time.Minute})
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	// The tenant and its property outlets are mirrored from auth-api first (the same sync the API runs
	// on first sight of a tenant), so a tenant nobody has opened in Maskani yet can still be seeded and
	// the demo property links to its auth-api outlet. This projection is the only write a dry run makes.
	syncer := tenant.NewSyncer(client, cfg.Auth.APIURL, db, zl)
	if _, err := uuid.Parse(strings.TrimSpace(*tenantArg)); err != nil {
		if _, serr := syncer.SyncTenant(ctx, strings.ToLower(strings.TrimSpace(*tenantArg))); serr != nil {
			zl.Warn("tenant sync from auth-api failed; using the local projection", zap.Error(serr))
		}
	}
	t, err := findTenant(ctx, client, strings.TrimSpace(*tenantArg))
	if err != nil {
		log.Fatalf("tenant %q is not in auth-api or the local tenants projection (%v)", *tenantArg, err)
	}
	if err := syncer.SyncOutlets(ctx, t.ID, t.Slug); err != nil {
		zl.Warn("outlet sync from auth-api failed; the property may stay unlinked", zap.Error(err))
	}
	box, err := secure.NewBox(cfg.Security.FieldEncryptionKey)
	if err != nil {
		log.Fatalf("FIELD_ENCRYPTION_KEY: %v", err)
	}

	// With --dry-run the clients get empty URLs, so even an accidental call cannot reach a service.
	tURL, aURL, nURL := cfg.Services.TreasuryURL, cfg.Auth.APIURL, cfg.Services.NotificationsURL
	if *dry {
		tURL, aURL, nURL = "", "", ""
	}
	tc := treasury.NewClient(tURL, cfg.Auth.APIKey, zl)
	ac := authapi.NewClient(aURL, cfg.Auth.APIKey, zl)
	nt := notify.NewClient(nURL, cfg.Auth.APIKey, zl)
	seq := sequence.NewAllocator(client)
	acc := accounts.NewService(client, tc, zl)

	s := &seeder{dry: *dry, ctx: tenantguard.With(ctx, t.ID), client: client, box: box, loc: loc, log: zl,
		tenantID: t.ID, slug: t.Slug, bearer: strings.TrimSpace(os.Getenv("SEED_BEARER_TOKEN")),
		settings: settings.NewService(client, zl), register: register.NewService(client, ac, acc, box, zl),
		billing: billing.NewService(client, tc, acc, loc, zl), util: utilities.NewService(client, zl),
		sales: sales.NewService(client, tc, acc, seq, loc, zl), works: works.NewService(client, seq, zl),
		gate: gate.NewService(client, box, seq, zl), notices: notices.NewService(client, nt, loc, zl)}
	if s.bearer != "" && !strings.HasPrefix(strings.ToLower(s.bearer), "bearer ") {
		s.bearer = "Bearer " + s.bearer
	}
	s.resident = residentFromEnv()
	s.portalURL = strings.TrimRight(cfg.HTTP.AppURL, "/")
	if s.resident.Phone != "" {
		fmt.Printf("demo resident: %s takes over the owner of %s and is invited to the owner portal\n", s.resident.Name, residentUnit)
	}

	start := time.Now()
	mode := "seeding"
	if s.dry {
		mode = "dry run (nothing is written)"
	}
	fmt.Printf("Shaba Village demo, tenant %s (%s), %s\n", t.Slug, t.ID, mode)
	if err := s.run(strings.ToUpper(strings.TrimSpace(*code))); err != nil {
		log.Fatalf("seed stopped: %v (re-run is safe; finished steps are skipped)", err)
	}
	if !s.dry && !*keepEvents {
		// Demo phones are fake and the dataset is not news: mark this run's outbox rows delivered so
		// notifications sends nothing. unit_account.created stays, treasury uses it for routes.
		res, err := db.ExecContext(ctx, `UPDATE outbox_events SET status = 'PUBLISHED', published_at = now(),
			error_message = 'suppressed by seed-tenant' WHERE tenant_id = $1 AND status = 'PENDING'
			AND created_at >= $2 AND event_type <> 'unit_account.created'`, t.ID, start.Add(-time.Second))
		if err != nil {
			zl.Warn("could not suppress seed events", zap.Error(err))
		} else if n, _ := res.RowsAffected(); n > 0 {
			fmt.Printf("suppressed %d outbox events raised by the seed (use --keep-events to deliver them)\n", n)
		}
	}
	verb := "created"
	if s.dry {
		verb = "would create"
	}
	fmt.Printf("done: %s %d, already present %d\n", verb, s.created, s.existing)
}

func findTenant(ctx context.Context, client *ent.Client, arg string) (*ent.Tenant, error) {
	if id, err := uuid.Parse(arg); err == nil {
		return client.Tenant.Get(ctx, id)
	}
	return client.Tenant.Query().Where(enttenant.Slug(strings.ToLower(arg))).Only(ctx)
}

// step reports one natural-key decision. It returns true when the caller should create.
func (s *seeder) step(found bool, format string, args ...any) bool {
	what := fmt.Sprintf(format, args...)
	if found {
		s.existing++
		return false
	}
	s.created++
	if s.dry {
		fmt.Println("  would create", what)
		return false
	}
	fmt.Println("  create", what)
	return true
}

// warn logs a failure that must not stop the run (S2S calls, optional steps).
func (s *seeder) warn(what string, err error) {
	if err != nil {
		s.log.Warn(what+" failed; continuing", zap.Error(err))
	}
}

func ptr[T any](v T) *T { return &v }

func (s *seeder) run(code string) error {
	if err := s.tenantSetup(); err != nil {
		return err
	}
	prop, err := s.property(code)
	if err != nil {
		return err
	}
	blocks, err := s.blocks(prop)
	if err != nil {
		return err
	}
	units, err := s.units(prop, blocks)
	if err != nil {
		return err
	}
	parties, err := s.parties()
	if err != nil {
		return err
	}
	if err := s.ownership(units, parties); err != nil {
		return err
	}
	if err := s.inviteResident(parties); err != nil {
		return err
	}
	if err := s.charges(prop); err != nil {
		return err
	}
	if err := s.meters(prop, units); err != nil {
		return err
	}
	if err := s.salesData(prop, units, parties); err != nil {
		return err
	}
	if err := s.vendors(prop); err != nil {
		return err
	}
	if err := s.workOrders(prop, units); err != nil {
		return err
	}
	if err := s.passes(prop, units); err != nil {
		return err
	}
	return s.notice(prop)
}

// tenantSetup makes sure the estate preset's funds, charges and modules exist.
func (s *seeder) tenantSetup() error {
	fmt.Println("tenant setup")
	hasEstate, _ := s.client.Fund.Query().Where(fund.Code("estate")).Exist(s.ctx)
	hasSales, _ := s.client.Fund.Query().Where(fund.Code("sales")).Exist(s.ctx)
	if s.dry {
		s.step(hasEstate, "fund estate (account prefix none)")
		s.step(hasSales, "fund sales (account prefix S-)")
		fmt.Println("  would ensure modules of the estate_developer preset and the estate charges")
		return nil
	}
	if err := s.settings.EnsureTenantDefaults(s.ctx, s.tenantID); err != nil {
		return fmt.Errorf("tenant defaults: %w", err)
	}
	for _, f := range settings.FundDefaults {
		if f.Code != "estate" && f.Code != "sales" {
			continue
		}
		err := s.client.Fund.Create().SetCode(f.Code).SetName(f.Name).SetKind(fund.Kind(f.Kind)).
			SetAccountPrefix(f.Prefix).SetIsDefault(f.Default).
			OnConflictColumns(fund.FieldTenantID, fund.FieldCode).DoNothing().Exec(s.ctx)
		if err != nil && err.Error() != "sql: no rows in result set" {
			return fmt.Errorf("fund %s: %w", f.Code, err)
		}
	}
	for i, c := range []string{"service_charge", "water", "garbage", "sinking_fund", "reservation_fee", "sale_deposit", "instalment"} {
		if err := s.settings.EnableCharge(s.ctx, s.tenantID, c, i); err != nil {
			return err
		}
	}
	current, err := s.settings.Modules(s.ctx, s.tenantID)
	if err != nil {
		return err
	}
	want := append([]string{}, settings.Presets["estate_developer"]...)
	missing := false
	for _, m := range want {
		if !current[m] {
			missing = true
		}
	}
	if missing {
		for m := range current {
			want = append(want, m)
		}
		fmt.Println("  enable the estate_developer modules (existing modules kept)")
		return s.settings.SetModules(s.ctx, s.tenantID, want, uuid.Nil)
	}
	return nil
}

func (s *seeder) property(code string) (*ent.Property, error) {
	fmt.Println("property")
	p, err := s.client.Property.Query().Where(property.Code(code)).Only(s.ctx)
	if err == nil {
		s.step(true, "")
		return p, nil
	}
	if !ent.IsNotFound(err) {
		return nil, err
	}
	if !s.step(false, "property %s Shaba Village (Syokimau, Machakos), published as shaba-village", code) {
		return &ent.Property{ID: uuid.Nil, Code: code}, nil
	}
	in := register.PropertyInput{Code: ptr(code), Name: ptr("Shaba Village"), PropertyType: ptr("estate"),
		UseCase: ptr("estate_developer"), Description: ptr("A gated estate of 40 two and three bedroom apartments in Syokimau, with borehole water, CCTV and a children's playground."),
		AddressLine: ptr("Off Mombasa Road, Syokimau"), Area: ptr("Syokimau"), Town: ptr("Syokimau"), County: ptr("Machakos"),
		Latitude: ptr(-1.3644), Longitude: ptr(36.9330), Phases: []string{"Phase 1"},
		Amenities: []string{"borehole", "perimeter_wall", "cctv", "playground", "parking"},
		Published: ptr(true), PublicSlug: ptr("shaba-village")}
	p, err = s.register.CreateProperty(s.ctx, s.slug, s.bearer, uuid.Nil, in)
	if err != nil {
		return nil, err
	}
	if p.OutletID == nil {
		// No admin token: link an outlet already synced from auth-api with the same code, if any.
		if o, oerr := s.client.Outlet.Query().Where(outlet.TenantID(s.tenantID), outlet.Code(code)).Only(s.ctx); oerr == nil {
			p, err = p.Update().SetOutletID(o.ID).Save(s.ctx)
			if err != nil {
				return nil, err
			}
		} else {
			fmt.Println("  note: property has no auth-api outlet yet (set SEED_BEARER_TOKEN to a tenant admin token to create one); staff with all-property access still see it")
		}
	}
	return p, nil
}

func (s *seeder) blocks(p *ent.Property) (map[string]uuid.UUID, error) {
	fmt.Println("blocks")
	out := map[string]uuid.UUID{}
	for i, code := range []string{"A", "B"} {
		b, err := s.client.Block.Query().Where(block.PropertyID(p.ID), block.Code(code)).Only(s.ctx)
		if err == nil {
			s.step(true, "")
			out[code] = b.ID
			continue
		}
		if !s.step(false, "block %s", code) {
			continue
		}
		b, err = s.register.CreateBlock(s.ctx, p.ID, register.BlockInput{Code: code, Name: "Block " + code,
			Phase: ptr("Phase 1"), Floors: ptr(5), Sort: ptr(i)})
		if err != nil {
			return nil, err
		}
		out[code] = b.ID
	}
	return out, nil
}

func (s *seeder) units(p *ent.Property, blocks map[string]uuid.UUID) (map[string]*ent.Unit, error) {
	fmt.Println("units")
	out := map[string]*ent.Unit{}
	handedOver := time.Date(2025, 6, 1, 0, 0, 0, 0, s.loc)
	for _, spec := range demoUnits() {
		u, err := s.client.Unit.Query().Where(unit.PropertyID(p.ID), unit.Code(spec.Code)).Only(s.ctx)
		if err == nil {
			s.step(true, "")
			out[spec.Code] = u
			continue
		}
		if !s.step(false, "unit %s (%s, %.0f sqm, %s)", spec.Code, spec.UnitType, spec.SizeSqm, spec.SaleStatus) {
			continue
		}
		sale := spec.SaleStatus
		if sale != "handed_over" {
			sale = "available" // reservation and contracts move these on
		}
		in := register.UnitInput{PropertyID: &p.ID, Code: ptr(spec.Code), UnitType: ptr(spec.UnitType), Use: ptr("residential"),
			Bedrooms: ptr(spec.Bedrooms), Bathrooms: ptr(spec.Bathrooms), SizeSqm: ptr(spec.SizeSqm), Floor: ptr(fmt.Sprintf("%d", (spec.Walking-1)%20/4)),
			Entitlement: ptr(spec.SizeSqm / 3900 * 100), ParkingBays: ptr(1), Phase: ptr("Phase 1"), SaleStatus: ptr(sale),
			OccupancyStatus: ptr(spec.Occupancy), WalkingOrder: ptr(spec.Walking), Features: []string{"balcony", "fitted kitchen", "borehole water"}}
		if id, ok := blocks[spec.Block]; ok {
			in.BlockID = &id
		}
		u, err = s.register.CreateUnit(s.ctx, uuid.Nil, in)
		if err != nil {
			return nil, fmt.Errorf("unit %s: %w", spec.Code, err)
		}
		if spec.SaleStatus == "handed_over" {
			if u, err = u.Update().SetHandedOverAt(handedOver).Save(s.ctx); err != nil {
				return nil, err
			}
		}
		out[spec.Code] = u
	}
	return out, nil
}

func (s *seeder) parties() ([]*ent.Party, error) {
	fmt.Println("owners and buyers")
	out := make([]*ent.Party, len(ownerNames))
	residentIdx := residentOwnerIdx()
	for i, name := range ownerNames {
		rawPhone, email := demoPhone(i), ""
		isResident := i == residentIdx && s.resident.Phone != ""
		if isResident {
			name, rawPhone, email = s.resident.Name, s.resident.Phone, s.resident.Email
		}
		phone := secure.NormalizePhone(rawPhone)
		p, err := s.client.Party.Query().Where(party.PhoneHash(s.box.Hash(phone))).First(s.ctx)
		if err == nil {
			s.step(true, "")
			out[i] = p
			continue
		}
		label := rawPhone
		if isResident {
			label = "(demo resident, phone from SEED_DEMO_RESIDENT_PHONE)"
		}
		if !s.step(false, "party %s %s", name, label) {
			continue
		}
		parts := strings.Fields(name)
		in := register.PartyInput{DisplayName: ptr(name), FirstName: ptr(parts[0]), LastName: ptr(parts[len(parts)-1]),
			Phone: ptr(rawPhone), PreferredChannel: ptr("whatsapp"), Kind: ptr("person")}
		if email != "" {
			in.Email, in.PreferredChannel = ptr(email), ptr("email")
		}
		p, err = s.register.CreateParty(s.ctx, uuid.Nil, in)
		if err != nil {
			return nil, err
		}
		out[i] = p
	}
	return out, nil
}

// inviteResident gives the demo resident an auth-api member with the maskani_owner role (added to,
// never replacing, any roles they already hold), so they can sign in to the owner portal with a
// phone code sent to their email or WhatsApp. The other demo owners have fake phones and are not
// invited. Skipped in a dry run and when the resident is already invited.
func (s *seeder) inviteResident(parties []*ent.Party) error {
	if s.resident.Phone == "" {
		return nil
	}
	fmt.Println("demo resident portal access")
	idx := residentOwnerIdx()
	if idx < 0 || idx >= len(parties) || parties[idx] == nil {
		s.step(false, "owner portal invite for the %s owner", residentUnit)
		return nil
	}
	p := parties[idx]
	if !s.step(p.AuthUserID != nil, "owner portal invite for %s (%s)", p.DisplayName, residentUnit) {
		return nil
	}
	if _, err := s.register.Invite(s.ctx, p.ID, s.slug, s.portalURL); err != nil {
		// Without auth-api the resident cannot sign in, so this one is not optional.
		return fmt.Errorf("invite the demo resident: %w", err)
	}
	return nil
}

// ownership links each sold unit to its owner with bill-to (opens the estate account; the paybill
// route is registered with treasury by the accounts service, and retried by the API job on failure).
func (s *seeder) ownership(units map[string]*ent.Unit, parties []*ent.Party) error {
	fmt.Println("ownership")
	for _, spec := range demoUnits() {
		if spec.OwnerIdx < 0 {
			continue
		}
		u, p := units[spec.Code], parties[spec.OwnerIdx]
		if u == nil || p == nil {
			s.step(false, "owner link %s to %s and estate account %s", spec.Code, ownerNames[spec.OwnerIdx], spec.Code)
			continue
		}
		found, err := s.client.UnitParty.Query().Where(unitparty.UnitID(u.ID), unitparty.PartyID(p.ID),
			unitparty.RoleEQ(unitparty.RoleOwner), unitparty.StatusEQ(unitparty.StatusActive)).Exist(s.ctx)
		if err != nil {
			return err
		}
		if !s.step(found, "owner link %s to %s and estate account %s", spec.Code, p.DisplayName, spec.Code) {
			continue
		}
		if _, err := s.register.LinkParty(s.ctx, u.ID, uuid.Nil, register.LinkInput{PartyID: p.ID, Role: "owner", IsPrimary: true,
			StartDate: u.HandedOverAt, BillTo: []string{"service_charge", "water", "garbage", "sinking_fund"}, Source: "import"}); err != nil {
			return fmt.Errorf("link %s: %w", spec.Code, err)
		}
		// Linking an owner marks a vacant unit owner occupied; restore the planned occupancy.
		if _, err := s.register.UpdateUnit(s.ctx, u.ID, register.UnitInput{OccupancyStatus: ptr(spec.Occupancy)}); err != nil {
			return err
		}
	}
	return nil
}

// charges sets the SRDD 8.2 rates behind the B07 worked example (compute_test.go): service charge
// 3,500 (2BR) and 4,500 (3BR), water 150 per m3, garbage 300, sinking fund 300 for the property.
// B07 with 9 m3 bills 4,500 + 1,350 + 300 + 300 = 6,450.
func (s *seeder) charges(p *ent.Property) error {
	fmt.Println("charge rates")
	type rateSpec struct {
		code, scope, unitType string
		amount                float64
	}
	for _, r := range []rateSpec{
		{"service_charge", "unit_type", "2br_apartment", 3500},
		{"service_charge", "unit_type", "3br_apartment", 4500},
		{"water", "tenant", "", 150},
		{"garbage", "tenant", "", 300},
		{"sinking_fund", "property", "", 300},
	} {
		ct, err := s.client.ChargeType.Query().Where(chargetype.Code(r.code)).Only(s.ctx)
		if err != nil {
			s.step(false, "rate %s %s %s %.0f (charge enabled with the tenant defaults)", r.code, r.scope, r.unitType, r.amount)
			continue
		}
		q := s.client.ChargeRate.Query().Where(chargerate.ChargeTypeID(ct.ID), chargerate.ScopeEQ(chargerate.Scope(r.scope)),
			chargerate.EffectiveFrom(rateFrom), chargerate.EffectiveToIsNil())
		switch r.scope {
		case "unit_type":
			q = q.Where(chargerate.UnitType(r.unitType))
		case "property":
			q = q.Where(chargerate.PropertyID(p.ID))
		}
		found, err := q.Exist(s.ctx)
		if err != nil {
			return err
		}
		if !s.step(found, "rate %s %s %s %.0f from %s", r.code, r.scope, r.unitType, r.amount, rateFrom.Format("2006-01-02")) {
			continue
		}
		in := billing.RateInput{Scope: r.scope, UnitType: r.unitType, Amount: r.amount, EffectiveFrom: rateFrom, Notes: "Shaba Village demo tariff"}
		if r.scope == "property" {
			in.PropertyID = &p.ID
		}
		if _, err := s.billing.AddRate(s.ctx, ct.ID, uuid.Nil, in); err != nil {
			return fmt.Errorf("rate %s: %w", r.code, err)
		}
	}
	return nil
}

// meters creates a water meter per unit plus the borehole meter, and records last month's round.
func (s *seeder) meters(p *ent.Property, units map[string]*ent.Unit) error {
	period := lastPeriod(time.Now(), s.loc)
	fmt.Println("meters and readings for", period)
	if !s.dry && p.ID != uuid.Nil {
		if _, err := s.util.GetRound(s.ctx, p.ID, period); err != nil {
			return err
		}
	}
	total := 0.0
	for _, spec := range demoUnits() {
		u := units[spec.Code]
		serial := "SHB-W-" + spec.Code
		opening, reading := demoReading(spec)
		total += reading - opening
		m, err := s.client.Meter.Query().Where(meter.Serial(serial)).Only(s.ctx)
		if err != nil && !ent.IsNotFound(err) {
			return err
		}
		if m == nil {
			if !s.step(false, "meter %s on %s (opening %.0f)", serial, spec.Code, opening) || u == nil {
				s.step(false, "reading %s %s: %.0f", serial, period, reading)
				continue
			}
			m, err = s.util.CreateMeter(s.ctx, utilities.MeterInput{PropertyID: p.ID, UnitID: &u.ID, Kind: "unit", Utility: "water",
				Serial: serial, Make: "Itron", LocationNote: "Ground floor meter bank, block " + spec.Block,
				InitialReading: opening, Multiplier: 1, WalkingOrder: spec.Walking})
			if err != nil {
				return err
			}
		} else {
			s.step(true, "")
		}
		if err := s.reading(m, period, reading); err != nil {
			return err
		}
	}
	// Borehole supply: billed water plus about 12 percent unaccounted, so the water balance shows a loss.
	bm, err := s.client.Meter.Query().Where(meter.Serial("SHB-BH-01")).Only(s.ctx)
	if err != nil && !ent.IsNotFound(err) {
		return err
	}
	if bm == nil {
		if !s.step(false, "borehole meter SHB-BH-01") {
			return nil
		}
		if bm, err = s.util.CreateMeter(s.ctx, utilities.MeterInput{PropertyID: p.ID, Kind: "borehole", Utility: "water",
			Serial: "SHB-BH-01", Make: "Sensus", LocationNote: "Borehole pump house", InitialReading: 50000, Multiplier: 1}); err != nil {
			return err
		}
	} else {
		s.step(true, "")
	}
	return s.reading(bm, period, 50000+float64(int(total*1.12+0.5)))
}

func (s *seeder) reading(m *ent.Meter, period string, value float64) error {
	found, err := s.client.MeterReading.Query().Where(meterreading.MeterID(m.ID), meterreading.Period(period),
		meterreading.SourceEQ(meterreading.SourceRound)).Exist(s.ctx)
	if err != nil {
		return err
	}
	if !s.step(found, "reading %s %s: %.0f", m.Serial, period, value) {
		return nil
	}
	_, err = s.util.Record(s.ctx, m.ID, uuid.Nil, utilities.ReadingInput{Period: period, Reading: value,
		PhotoKey: "demo/meter-reading.jpg", ReadAt: ptr(time.Now().AddDate(0, 0, -5)), Notes: "demo round"})
	return err
}

// salesData creates the price list, one reservation and two activated contracts with schedules.
func (s *seeder) salesData(p *ent.Property, units map[string]*ent.Unit, parties []*ent.Party) error {
	fmt.Println("sales")
	const listName = "Shaba Village launch prices 2026"
	found, err := s.client.PriceList.Query().Where(pricelist.PropertyID(p.ID), pricelist.Name(listName)).Exist(s.ctx)
	if err != nil {
		return err
	}
	if s.step(found, "price list %q (2BR 6,500,000, 3BR 8,500,000, reservation fee 50,000, deposit 10%%)", listName) {
		if _, err := s.sales.CreatePriceList(s.ctx, uuid.Nil, sales.PriceListInput{PropertyID: p.ID, Name: listName,
			Phase: "Phase 1", EffectiveFrom: rateFrom, Activate: true, Items: []sales.PriceItemSpec{
				{UnitType: "2br_apartment", Price: 6500000, ReservationFee: 50000, DepositPct: 10, MaxTermMonths: 24},
				{UnitType: "3br_apartment", Price: 8500000, ReservationFee: 50000, DepositPct: 10, MaxTermMonths: 36},
			}}); err != nil {
			return err
		}
	}

	// Reservation of A16.
	if u, b := units["A16"], parties[buyerReservation]; u != nil && b != nil {
		has, err := s.client.Reservation.Query().Where(reservation.UnitID(u.ID)).Exist(s.ctx)
		if err != nil {
			return err
		}
		if s.step(has, "reservation of A16 for %s (fee invoiced through treasury)", b.DisplayName) {
			_, err := s.sales.Reserve(s.ctx, uuid.Nil, sales.ReserveInput{UnitID: u.ID, PartyID: b.ID, Days: 14})
			s.warn("reservation of A16", err)
		}
	} else {
		s.step(false, "reservation of A16 for %s", ownerNames[buyerReservation])
	}

	type contractSpec struct {
		unit   string
		buyer  int
		option string
		freq   string
		term   int
		ms     []sales.Milestone
	}
	for _, c := range []contractSpec{
		{"A15", buyerInstalments, "instalments", "monthly", 24, nil},
		{"B15", buyerMilestone, "milestone", "milestone", 0, []sales.Milestone{{Label: "Foundation", Pct: 30}, {Label: "Roofing", Pct: 40}, {Label: "Completion", Pct: 30}}},
	} {
		u, b := units[c.unit], parties[c.buyer]
		if u == nil || b == nil {
			s.step(false, "%s sale contract for %s on %s, activated with its schedule", c.option, ownerNames[c.buyer], c.unit)
			continue
		}
		existing, err := s.client.SaleContract.Query().Where(salecontract.UnitID(u.ID),
			salecontract.StatusNotIn(salecontract.StatusCancelled, salecontract.StatusTerminated)).First(s.ctx)
		if err != nil && !ent.IsNotFound(err) {
			return err
		}
		if existing != nil {
			s.step(true, "")
			if existing.Status == salecontract.StatusDraft {
				_, err := s.sales.Activate(s.ctx, existing.ID, time.Now().AddDate(0, -2, 0))
				s.warn("activating contract on "+c.unit, err)
			}
			continue
		}
		if !s.step(false, "%s sale contract for %s on %s, activated with its schedule", c.option, b.DisplayName, c.unit) {
			continue
		}
		sc, err := s.sales.CreateContract(s.ctx, uuid.Nil, sales.ContractInput{UnitID: u.ID, BuyerID: b.ID, PaymentOption: c.option,
			Frequency: c.freq, TermMonths: c.term, Milestones: c.ms, GraceDays: 30, Notes: "Shaba Village demo contract"})
		if err != nil {
			return fmt.Errorf("contract %s: %w", c.unit, err)
		}
		// Activation writes the schedule locally first; the invoices it raises for instalments already
		// due go to treasury and are logged, not fatal, when treasury is unreachable.
		_, err = s.sales.Activate(s.ctx, sc.ID, time.Now().AddDate(0, -2, 0))
		s.warn("activating contract on "+c.unit, err)
	}
	return nil
}

// vendors adds the security, cleaning and plumbing vendors with documents and guards.
func (s *seeder) vendors(p *ent.Property) error {
	fmt.Println("vendors")
	now := time.Now()
	type docSpec struct {
		docType, number string
		expires         time.Time
	}
	type vendorSpec struct {
		name, contact, cat string
		docs               []docSpec
		guards             [][2]string
	}
	for i, v := range []vendorSpec{
		{"Shaba Guard Services Ltd", "Peter Mwangi", "security", []docSpec{
			{"psra", "PSRA/2026/0412", now.AddDate(0, 0, 20)}, // expires within 30 days: shows on the dashboard
			{"business_permit", "MCG-BP-88213", now.AddDate(0, 8, 0)},
			{"kra", "P051234567X", now.AddDate(2, 0, 0)},
		}, [][2]string{{"SHB-G01", "John Kilonzo"}, {"SHB-G02", "Mary Atieno"}}},
		{"Sparkle Estate Cleaners", "Grace Njoki", "cleaning", []docSpec{
			{"business_permit", "MCG-BP-77102", now.AddDate(0, 6, 0)},
			{"insurance", "JUB-PL-55120", now.AddDate(1, 0, 0)},
		}, nil},
		{"Mavoko Plumbing Works", "Samuel Kyalo", "plumbing_electrical", []docSpec{
			{"nca", "NCA-8-2291", now.AddDate(0, 10, 0)},
		}, nil},
	} {
		ven, err := s.client.Vendor.Query().Where(vendor.Name(v.name)).First(s.ctx)
		if err != nil && !ent.IsNotFound(err) {
			return err
		}
		if ven == nil {
			if !s.step(false, "vendor %s (%s) with %d documents", v.name, v.cat, len(v.docs)) {
				continue
			}
			if ven, err = s.works.CreateVendor(s.ctx, uuid.Nil, works.VendorInput{Name: v.name, Categories: []string{v.cat},
				ContactName: v.contact, Phone: demoPhone(100 + i)}); err != nil {
				return err
			}
		} else {
			s.step(true, "")
		}
		for _, d := range v.docs {
			has, err := s.client.VendorDocument.Query().Where(vendordocument.VendorID(ven.ID), vendordocument.DocType(d.docType)).Exist(s.ctx)
			if err != nil {
				return err
			}
			if s.step(has, "document %s for %s", d.docType, v.name) {
				if _, err := s.works.AddDocument(s.ctx, ven.ID, works.DocumentInput{DocType: d.docType, Number: d.number,
					IssuedAt: ptr(d.expires.AddDate(-1, 0, 0)), ExpiresAt: ptr(d.expires)}); err != nil {
					return err
				}
			}
		}
		for gi, g := range v.guards {
			pr, err := s.client.VendorPersonnel.Query().Where(vendorpersonnel.BadgeNumber(g[0])).Only(s.ctx)
			if err != nil && !ent.IsNotFound(err) {
				return err
			}
			if pr == nil {
				if !s.step(false, "guard %s badge %s with demo gate PIN", g[1], g[0]) {
					continue
				}
				if pr, err = s.works.AddPersonnel(s.ctx, ven.ID, g[1], "guard", demoPhone(110+gi), g[0], []string{p.ID.String()}); err != nil {
					return err
				}
			} else {
				s.step(true, "")
			}
			if pr.PinHash == "" && !s.dry {
				// Demo PIN 1234 for every guard; managers change it from the console.
				_, err := s.works.SetGuardPIN(s.ctx, ven.ID, pr.ID, "1234")
				s.warn("guard PIN", err)
			}
		}
	}
	return nil
}

// workOrders raises four work orders and moves them to different statuses.
func (s *seeder) workOrders(p *ent.Property, units map[string]*ent.Unit) error {
	fmt.Println("work orders")
	plumber, _ := s.client.Vendor.Query().Where(vendor.Name("Mavoko Plumbing Works")).First(s.ctx)
	type woSpec struct {
		title, cat, prio, unit, area string
		actions                      []string
	}
	actor := works.Actor{Kind: "staff"}
	for _, w := range []woSpec{
		{"Leaking kitchen sink", "plumbing", "normal", "B07", "", nil},
		{"Burst pipe near block A meter bank", "water_supply", "high", "", "Block A meter bank", []string{"assign"}},
		{"Gate motor slow to open", "security_systems", "high", "", "Main gate", []string{"assign", "start"}},
		{"Corridor lights out, block B third floor", "electrical", "normal", "", "Block B, floor 3", []string{"assign", "start", "complete"}},
	} {
		found, err := s.client.WorkOrder.Query().Where(workorder.PropertyID(p.ID), workorder.Title(w.title)).Exist(s.ctx)
		if err != nil {
			return err
		}
		if !s.step(found, "work order %q (%s)", w.title, strings.Join(append([]string{"triaged"}, w.actions...), " > ")) {
			continue
		}
		in := works.RequestInput{PropertyID: p.ID, Area: w.area, Category: w.cat, Priority: w.prio, Title: w.title,
			Description: "Demo work order"}
		if u := units[w.unit]; u != nil {
			in.UnitID = &u.ID
		}
		wo, err := s.works.Create(s.ctx, actor, in)
		if err != nil {
			return err
		}
		for _, a := range w.actions {
			act := works.ActionInput{Action: a, Note: "demo"}
			if a == "assign" {
				if plumber == nil {
					break
				}
				act.VendorID = &plumber.ID
			}
			if a == "complete" {
				act.CostAmount, act.MinutesOnSite = ptr(3500.0), ptr(90)
			}
			if _, err := s.works.Act(s.ctx, wo.ID, actor, act); err != nil {
				return fmt.Errorf("work order %q %s: %w", w.title, a, err)
			}
		}
	}
	return nil
}

// passes issues three visitor passes for B07 (no visitor phones, so no codes are messaged).
func (s *seeder) passes(p *ent.Property, units map[string]*ent.Unit) error {
	fmt.Println("visitor passes")
	host := units["B07"]
	now := time.Now()
	for _, v := range []struct {
		name, kind string
		hours      int
	}{{"Brian Otieno", "guest_single", 12}, {"Esther Wairimu", "domestic_staff", 24 * 60}, {"Glovo delivery", "delivery", 2}} {
		found, err := s.client.VisitorPass.Query().Where(visitorpass.PropertyID(p.ID), visitorpass.VisitorName(v.name),
			visitorpass.StatusEQ(visitorpass.StatusActive), visitorpass.ValidToGT(now)).Exist(s.ctx)
		if err != nil {
			return err
		}
		if !s.step(found, "visitor pass %s (%s) for B07", v.name, v.kind) || host == nil {
			continue
		}
		if _, err := s.gate.CreatePass(s.ctx, "staff", uuid.Nil, nil, gate.PassInput{PropertyID: p.ID, UnitID: &host.ID,
			PassType: v.kind, VisitorName: v.name, ValidFrom: &now, ValidTo: ptr(now.Add(time.Duration(v.hours) * time.Hour)),
			Notes: "demo pass"}); err != nil {
			return err
		}
	}
	return nil
}

func (s *seeder) notice(p *ent.Property) error {
	fmt.Println("notices")
	const title = "Water interruption on Saturday"
	found, err := s.client.Notice.Query().Where(notice.PropertyID(p.ID), notice.Title(title)).Exist(s.ctx)
	if err != nil {
		return err
	}
	if !s.step(found, "draft notice %q", title) {
		return nil
	}
	_, err = s.notices.Create(s.ctx, uuid.Nil, s.slug, notices.Input{PropertyID: &p.ID, Category: "water", Title: title,
		Body:     "Water will be off from 9am to 1pm on Saturday while the borehole pump is serviced. Please store enough water.",
		Channels: []string{"whatsapp", "email"}})
	return err
}
