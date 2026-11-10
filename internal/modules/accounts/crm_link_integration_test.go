package accounts_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/Bengo-Hub/shared-service-client/crm"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/config"
	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/fund"
	"github.com/bengobox/maskani-api/internal/ent/outboxevent"
	"github.com/bengobox/maskani-api/internal/ent/party"
	_ "github.com/bengobox/maskani-api/internal/ent/runtime"
	"github.com/bengobox/maskani-api/internal/modules/accounts"
	"github.com/bengobox/maskani-api/internal/modules/privacy"
	"github.com/bengobox/maskani-api/internal/modules/register"
	"github.com/bengobox/maskani-api/internal/modules/treasury"
	"github.com/bengobox/maskani-api/internal/platform/database"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
	"github.com/bengobox/maskani-api/internal/shared/secure"
)

const testServiceKey = "test-internal-key"

// fakeCRM stands in for marketflow's internal contacts API and records every call.
type fakeCRM struct {
	mu      sync.Mutex
	status  int // answer for every call; 0 means success
	upserts []map[string]string
	raw     []string // upsert bodies as sent
	deletes []string // "<contact id>?<query>"
	nextID  uuid.UUID
}

func (f *fakeCRM) setStatus(code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = code
}

func (f *fakeCRM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("X-API-Key") != testServiceKey {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if f.status != 0 {
		w.WriteHeader(f.status)
		_, _ = w.Write([]byte(`{"error":"unavailable","code":"test"}`))
		return
	}
	const base = "/api/v1/internal/contacts"
	switch {
	case r.Method == http.MethodPost && r.URL.Path == base+"/upsert":
		raw, _ := io.ReadAll(r.Body)
		var body map[string]string
		_ = json.Unmarshal(raw, &body)
		f.upserts = append(f.upserts, body)
		f.raw = append(f.raw, string(raw))
		f.nextID = uuid.New()
		_ = json.NewEncoder(w).Encode(map[string]any{"id": f.nextID, "first_name": body["first_name"],
			"last_name": body["last_name"], "email": body["email"], "phone": body["phone"]})
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, base+"/"):
		f.deletes = append(f.deletes, strings.TrimPrefix(r.URL.Path, base+"/")+"?"+r.URL.RawQuery)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeCRM) lastID() uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nextID
}

func (f *fakeCRM) snapshot() (upserts []map[string]string, raw, deletes []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]string(nil), f.upserts...), append([]string(nil), f.raw...), append([]string(nil), f.deletes...)
}

type crmEnv struct {
	ctx    context.Context
	tenant uuid.UUID
	client *ent.Client
	fake   *fakeCRM
	crm    *crm.Client
	acc    *accounts.Service
	reg    *register.Service
	box    *secure.Box
}

// setupCRM opens the scratch database and a fresh tenant. Needs REGISTER_TEST_POSTGRES_URL, the
// same scratch database the register integration tests use.
func setupCRM(t *testing.T) *crmEnv {
	t.Helper()
	dsn := os.Getenv("REGISTER_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("REGISTER_TEST_POSTGRES_URL not set")
	}
	db, err := database.OpenSQL(dsn, config.PostgresConfig{MaxOpenConns: 2, MaxIdleConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake := &fakeCRM{}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	box, err := secure.NewBox("test-secret-key-for-crm-link-0123456789")
	if err != nil {
		t.Fatal(err)
	}
	cc := crm.New(srv.URL, testServiceKey, zap.NewNop())
	acc := accounts.NewService(client, treasury.NewClient("", "", zap.NewNop()), zap.NewNop())
	acc.SetCRM(cc)
	tenant := uuid.New()
	return &crmEnv{
		ctx: tenantguard.With(context.Background(), tenant), tenant: tenant, client: client, fake: fake, crm: cc,
		acc: acc, reg: register.NewService(client, nil, acc, box, zap.NewNop()), box: box,
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// unitWithFunds creates a property, a unit and the estate and sales funds.
func (e *crmEnv) unitWithFunds(t *testing.T, code string) *ent.Unit {
	t.Helper()
	prop, err := e.client.Property.Create().SetCode("P" + code).SetName("Estate " + code).Save(e.ctx)
	must(t, err)
	u, err := e.client.Unit.Create().SetPropertyID(prop.ID).SetCode(code).Save(e.ctx)
	must(t, err)
	// As in production, the sales fund prefixes its refs ("S-B07"); without it the estate and
	// sales accounts of one unit would share a ref, which (tenant_id, account_ref) forbids.
	for _, f := range []struct{ code, prefix string }{{"estate", ""}, {"sales", "S-"}} {
		exists, err := e.client.Fund.Query().Where(fund.Code(f.code)).Exist(e.ctx)
		must(t, err)
		if !exists {
			_, err = e.client.Fund.Create().SetCode(f.code).SetName(f.code).SetKind(fund.Kind(f.code)).
				SetAccountPrefix(f.prefix).Save(e.ctx)
			must(t, err)
		}
	}
	return u
}

// sensitiveParty creates a person carrying every field that must never reach the CRM.
func (e *crmEnv) sensitiveParty(t *testing.T, first, last, phone, email string) *ent.Party {
	t.Helper()
	idEnc, err := e.box.Encrypt("12345678")
	must(t, err)
	pinEnc, err := e.box.Encrypt("A012345678Z")
	must(t, err)
	p, err := e.client.Party.Create().SetDisplayName(first + " " + last).SetFirstName(first).SetLastName(last).
		SetPhone(phone).SetEmail(email).SetIDType("national_id").SetNationalIDEnc(idEnc).SetKraPinEnc(pinEnc).
		SetNationality("Kenyan").SetPostalAddress("PO Box 1, Nairobi").
		SetConsents(map[string]any{"marketing": true}).Save(e.ctx)
	must(t, err)
	return p
}

// assertNothingSensitive fails when any upsert carried more than name, phone and email.
func assertNothingSensitive(t *testing.T, raw []string, upserts []map[string]string) {
	t.Helper()
	for _, body := range raw {
		for _, bad := range []string{"12345678", "A012345678Z", "Kenyan", "national", "kra", "PO Box", "consent", "marketing"} {
			if strings.Contains(body, bad) {
				t.Fatalf("an upsert carried %q: %s", bad, body)
			}
		}
	}
	allowed := map[string]bool{"tenant_id": true, "phone": true, "email": true, "first_name": true, "last_name": true}
	for _, u := range upserts {
		for k := range u {
			if !allowed[k] {
				t.Fatalf("an upsert carried the field %q", k)
			}
		}
	}
}

// TestBilledPartyLinked: making an owner the primary party links it to the CRM with name, phone
// and email only. A CRM outage never blocks the link to the unit, and the next account update
// links the party.
func TestBilledPartyLinked(t *testing.T) {
	e := setupCRM(t)
	u := e.unitWithFunds(t, "B07")
	p := e.sensitiveParty(t, "Jane", "Wanjiru", "254712000333", "jane@example.com")

	_, err := e.reg.LinkParty(e.ctx, u.ID, uuid.New(), register.LinkInput{PartyID: p.ID, Role: "owner", IsPrimary: true})
	must(t, err)
	got, err := e.client.Party.Get(e.ctx, p.ID)
	must(t, err)
	upserts, raw, _ := e.fake.snapshot()
	if len(upserts) != 1 {
		t.Fatalf("one upsert expected, got %d", len(upserts))
	}
	if got.CrmContactID == nil || *got.CrmContactID != e.fake.lastID() {
		t.Fatalf("party linked to the CRM contact: %v", got.CrmContactID)
	}
	if upserts[0]["tenant_id"] != e.tenant.String() || upserts[0]["first_name"] != "Jane" ||
		upserts[0]["last_name"] != "Wanjiru" || upserts[0]["email"] != "jane@example.com" || upserts[0]["phone"] == "" {
		t.Fatalf("upsert body: %v", upserts[0])
	}
	if got.Phone != "254712000333" {
		t.Fatal("the phone stays on the party")
	}

	// Already linked: another account for the same party sends nothing.
	_, err = e.acc.Ensure(e.ctx, u, "sales", &accounts.Party{ID: p.ID, Name: p.DisplayName, Role: "buyer"})
	must(t, err)
	if n, _, _ := e.fake.snapshot(); len(n) != 1 {
		t.Fatalf("a linked party is not sent again: %d upserts", len(n))
	}

	// A company owner while the CRM is down: the owner link and account still happen.
	u2 := e.unitWithFunds(t, "B08")
	co, err := e.client.Party.Create().SetKind(party.KindCompany).SetDisplayName("Acme").SetCompanyName("Acme Holdings Ltd").
		SetRegistrationNumber("PVT-123").SetPhone("254712000444").Save(e.ctx)
	must(t, err)
	e.fake.setStatus(http.StatusServiceUnavailable)
	link, err := e.reg.LinkParty(e.ctx, u2.ID, uuid.New(), register.LinkInput{PartyID: co.ID, Role: "owner", IsPrimary: true})
	must(t, err)
	if link == nil {
		t.Fatal("the owner link is saved during a CRM outage")
	}
	acc, err := e.acc.Ensure(e.ctx, u2, "estate", nil)
	must(t, err)
	if acc.PrimaryPartyID == nil || *acc.PrimaryPartyID != co.ID {
		t.Fatal("the account bills the company during a CRM outage")
	}
	gotCo, err := e.client.Party.Get(e.ctx, co.ID)
	must(t, err)
	if gotCo.CrmContactID != nil {
		t.Fatal("an outage leaves the party unlinked")
	}

	// The CRM is back: the next account update links the company under its company name.
	e.fake.setStatus(0)
	_, err = e.acc.Ensure(e.ctx, u2, "estate", &accounts.Party{ID: co.ID, Name: co.DisplayName, Phone: co.Phone})
	must(t, err)
	gotCo, err = e.client.Party.Get(e.ctx, co.ID)
	must(t, err)
	upserts, raw, _ = e.fake.snapshot()
	if gotCo.CrmContactID == nil || len(upserts) != 2 {
		t.Fatalf("lazy link after the outage: %v, %d upserts", gotCo.CrmContactID, len(upserts))
	}
	if upserts[1]["first_name"] != "Acme Holdings Ltd" || upserts[1]["last_name"] != "" || strings.Contains(raw[1], "PVT-123") {
		t.Fatalf("company upsert: %s", raw[1])
	}
	assertNothingSensitive(t, raw, upserts)
}

// TestHouseholdMemberNotLinked: household members and domestic staff are never sent to the CRM,
// even when they end up as an account's primary party.
func TestHouseholdMemberNotLinked(t *testing.T) {
	e := setupCRM(t)
	u := e.unitWithFunds(t, "C01")
	hm := e.sensitiveParty(t, "Kamau", "Njoroge", "254712000555", "kamau@example.com")
	_, err := e.client.UnitParty.Create().SetUnitID(u.ID).SetPartyID(hm.ID).SetRole("household_member").
		SetStartDate(time.Now()).Save(e.ctx)
	must(t, err)
	staff := e.sensitiveParty(t, "Mary", "Wanjiku", "254712000666", "")
	_, err = e.client.UnitParty.Create().SetUnitID(u.ID).SetPartyID(staff.ID).SetRole("domestic_staff").
		SetStartDate(time.Now()).Save(e.ctx)
	must(t, err)

	// No role given: the household link decides.
	acc, err := e.acc.Ensure(e.ctx, u, "estate", &accounts.Party{ID: hm.ID, Name: hm.DisplayName, Phone: hm.Phone})
	must(t, err)
	if acc.PrimaryPartyID == nil || *acc.PrimaryPartyID != hm.ID {
		t.Fatal("the account is still opened")
	}
	// An explicit non-billed role.
	_, err = e.acc.Ensure(e.ctx, u, "sales", &accounts.Party{ID: staff.ID, Name: staff.DisplayName, Role: "domestic_staff"})
	must(t, err)

	upserts, _, _ := e.fake.snapshot()
	if len(upserts) != 0 {
		t.Fatalf("no household party reaches the CRM: %v", upserts)
	}
	for _, id := range []uuid.UUID{hm.ID, staff.ID} {
		got, err := e.client.Party.Get(e.ctx, id)
		must(t, err)
		if got.CrmContactID != nil {
			t.Fatal("a household party has no CRM contact")
		}
	}
}

// TestAnonymiseUnlinksWithoutDeletingCRMContact: erasing a linked owner clears the party's
// contact id locally and never deletes the CRM contact, which other services may still use. An
// ids-only privacy.party_anonymised event tells the CRM owner.
func TestAnonymiseUnlinksWithoutDeletingCRMContact(t *testing.T) {
	e := setupCRM(t)
	priv := privacy.NewService(e.client, e.reg, e.box, zap.NewNop())
	u := e.unitWithFunds(t, "D04")
	p := e.sensitiveParty(t, "Achieng", "Otieno", "254712000777", "achieng@example.com")
	link, err := e.reg.LinkParty(e.ctx, u.ID, uuid.New(), register.LinkInput{PartyID: p.ID, Role: "owner", IsPrimary: true})
	must(t, err)
	linked, err := e.client.Party.Get(e.ctx, p.ID)
	must(t, err)
	if linked.CrmContactID == nil {
		t.Fatal("the owner is linked first")
	}
	contact := *linked.CrmContactID
	must(t, e.client.UnitParty.UpdateOneID(link.ID).SetStatus("ended").SetEndDate(time.Now()).Exec(e.ctx))

	req, err := priv.Create(e.ctx, privacy.Input{PartyID: p.ID, Kind: "deletion"}, "portal", uuid.New())
	must(t, err)
	_, err = priv.Anonymise(e.ctx, req.ID, uuid.New())
	must(t, err)
	_, _, deletes := e.fake.snapshot()
	if len(deletes) != 0 {
		t.Fatalf("maskani never deletes a CRM contact: %v", deletes)
	}
	gone, err := e.client.Party.Get(e.ctx, p.ID)
	must(t, err)
	if gone.CrmContactID != nil || gone.Status != party.StatusAnonymised || gone.Phone != "" {
		t.Fatalf("party anonymised and unlinked: %+v", gone)
	}
	// The outbox has no tenant guard and the scratch database keeps earlier runs, so scope to this
	// tenant and party (Publish uses the party id as the aggregate id).
	ev, err := e.client.OutboxEvent.Query().Where(outboxevent.TenantID(e.tenant),
		outboxevent.EventType(privacy.PartyAnonymised), outboxevent.AggregateID(p.ID.String())).Only(e.ctx)
	must(t, err)
	var envelope struct {
		Payload map[string]any `json:"payload"`
	}
	must(t, json.Unmarshal(ev.Payload, &envelope))
	if envelope.Payload["party_id"] != p.ID.String() || envelope.Payload["crm_contact_id"] != contact.String() ||
		envelope.Payload["tenant_id"] != e.tenant.String() || len(envelope.Payload) != 3 {
		t.Fatalf("the event carries the three ids only: %v", envelope.Payload)
	}
	for _, bad := range []string{"Achieng", "Otieno", "achieng@example.com", "254712000777"} {
		if strings.Contains(string(ev.Payload), bad) {
			t.Fatalf("the event carries %q: %s", bad, ev.Payload)
		}
	}
}
