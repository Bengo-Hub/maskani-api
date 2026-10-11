package privacy

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/config"
	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/outboxevent"
	"github.com/bengobox/maskani-api/internal/ent/party"
	"github.com/bengobox/maskani-api/internal/ent/privacyrequest"
	_ "github.com/bengobox/maskani-api/internal/ent/runtime"
	"github.com/bengobox/maskani-api/internal/modules/register"
	"github.com/bengobox/maskani-api/internal/platform/database"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
	"github.com/bengobox/maskani-api/internal/shared/otp"
	"github.com/bengobox/maskani-api/internal/shared/secure"
)

// TestPrivacyRequestsOnPostgres: an office request is verified by the code sent to the phone on
// record (wrong codes counted), exported as a zip, and an erasure waits until the person has no
// active unit link, then clears their details and keeps the account. Needs
// PRIVACY_TEST_POSTGRES_URL (a scratch database).
func TestPrivacyRequestsOnPostgres(t *testing.T) {
	dsn := os.Getenv("PRIVACY_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("PRIVACY_TEST_POSTGRES_URL not set")
	}
	db, err := database.OpenSQL(dsn, config.PostgresConfig{MaxOpenConns: 2, MaxIdleConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer client.Close()
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := tenantguard.With(context.Background(), uuid.New())
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	box, err := secure.NewBox("test-secret-key-for-privacy-0123456789")
	must(err)
	s := NewService(client, register.NewService(client, nil, nil, box, zap.NewNop()), box, zap.NewNop())
	staff := uuid.New()

	prop, err := client.Property.Create().SetCode("MV").SetName("Mvumo").Save(ctx)
	must(err)
	u, err := client.Unit.Create().SetPropertyID(prop.ID).SetCode("B07").Save(ctx)
	must(err)
	idEnc, err := box.Encrypt("12345678")
	must(err)
	p, err := client.Party.Create().SetDisplayName("Jane Wanjiru").SetPhone("254712000222").SetEmail("jane@example.com").
		SetNationalIDEnc(idEnc).Save(ctx)
	must(err)
	link, err := client.UnitParty.Create().SetUnitID(u.ID).SetPartyID(p.ID).SetRole("owner").SetStartDate(time.Now()).Save(ctx)
	must(err)
	fund, err := client.Fund.Create().SetCode("SC").SetName("Service charge").Save(ctx)
	must(err)
	acc, err := client.UnitAccount.Create().SetUnitID(u.ID).SetFundID(fund.ID).SetAccountRef("SC-B07").
		SetPrimaryPartyID(p.ID).SetCustomerName("Jane Wanjiru").SetCustomerPhone("254712000222").Save(ctx)
	must(err)
	_, err = client.Vehicle.Create().SetUnitID(u.ID).SetPartyID(p.ID).SetPlate("KDQ123A").Save(ctx)
	must(err)

	// Access, recorded by the office.
	req, err := s.Create(ctx, Input{PartyID: p.ID, Kind: "access"}, "office", staff)
	must(err)
	if req.Status != privacyrequest.StatusReceived || req.DueAt.Sub(req.ReceivedAt) != 7*24*time.Hour {
		t.Fatalf("office request: %s due %v", req.Status, req.DueAt.Sub(req.ReceivedAt))
	}
	if _, err := s.Create(ctx, Input{PartyID: p.ID, Kind: "access"}, "office", staff); err == nil {
		t.Fatal("one open request per kind")
	}
	if _, err := s.Export(ctx, req.ID, false); err == nil {
		t.Fatal("no export before verification")
	}
	must(s.SendCode(ctx, req.ID))
	ev, err := client.OutboxEvent.Query().Where(outboxevent.EventType(otp.Requested)).Only(ctx)
	must(err)
	var envelope struct {
		Payload map[string]any `json:"payload"`
	}
	must(json.Unmarshal(ev.Payload, &envelope))
	payload := envelope.Payload
	if payload["phone"] != "254712000222" {
		t.Fatalf("code goes to the phone on record: %v", payload["phone"])
	}
	code, _ := payload["otp"].(string)
	if _, err := s.Verify(ctx, req.ID, staff, "000000", false, ""); err == nil {
		t.Fatal("a wrong code fails")
	}
	got, err := s.Get(ctx, req.ID)
	must(err)
	if otp.Count(got.Metadata["otp_attempts"]) != 1 || got.Metadata["otp_hash"] != nil {
		t.Fatalf("attempt counted, hash hidden: %v", got.Metadata)
	}
	got, err = s.Verify(ctx, req.ID, staff, code, false, "")
	must(err)
	if got.Status != privacyrequest.StatusVerified || got.Metadata["verified_by"] != "code" {
		t.Fatalf("verified: %s %v", got.Status, got.Metadata["verified_by"])
	}
	if _, err := s.Export(ctx, req.ID, true); err == nil {
		t.Fatal("the person downloads only once completed")
	}
	f, err := s.Export(ctx, req.ID, false)
	must(err)
	zr, err := zip.NewReader(bytes.NewReader(f.Body), int64(len(f.Body)))
	must(err)
	names := map[string]bool{}
	for _, zf := range zr.File {
		names[zf.Name] = true
	}
	for _, want := range []string{"data.json", "units.csv", "accounts.csv", "vehicles.csv", "README.txt"} {
		if !names[want] {
			t.Fatalf("export lacks %s: %v", want, names)
		}
	}
	if bytes.Contains(f.Body, []byte("12345678")) {
		t.Fatal("the identity number must be masked")
	}
	_, err = s.Complete(ctx, req.ID, staff, "copy emailed")
	must(err)
	_, err = s.Export(ctx, req.ID, true)
	must(err)

	// Erasure from the portal: verified at once, refused while the person owns a unit.
	del, err := s.Create(ctx, Input{PartyID: p.ID, Kind: "deletion"}, "portal", uuid.New())
	must(err)
	if del.Status != privacyrequest.StatusVerified {
		t.Fatalf("portal request is verified by the session: %s", del.Status)
	}
	if _, err := s.Anonymise(ctx, del.ID, staff); err == nil {
		t.Fatal("an owner with an active link cannot be erased")
	}
	must(client.UnitParty.UpdateOneID(link.ID).SetStatus("ended").SetEndDate(time.Now()).Exec(ctx))
	done, err := s.Anonymise(ctx, del.ID, staff)
	must(err)
	if done.Status != privacyrequest.StatusCompleted {
		t.Fatalf("erasure completes the request: %s", done.Status)
	}
	gone, err := client.Party.Get(ctx, p.ID)
	must(err)
	if gone.Status != party.StatusAnonymised || gone.Phone != "" || gone.Email != "" || gone.NationalIDEnc != "" ||
		gone.DisplayName == "Jane Wanjiru" {
		t.Fatalf("details erased: %+v", gone)
	}
	a, err := client.UnitAccount.Get(ctx, acc.ID)
	must(err)
	if a.CustomerName == "Jane Wanjiru" || a.CustomerPhone != "" || a.AccountRef != "SC-B07" {
		t.Fatalf("account kept without the name: %+v", a)
	}
	if n, _ := client.Vehicle.Query().Count(ctx); n != 0 {
		t.Fatal("vehicles removed")
	}
	if n, _ := client.PrivacyRequest.Query().Where(privacyrequest.RequesterNameNEQ("")).Count(ctx); n != 0 {
		t.Fatal("requests keep no name")
	}
	if _, err := s.Create(ctx, Input{PartyID: p.ID, Kind: "access"}, "portal", uuid.New()); err == nil {
		t.Fatal("an erased person takes no new requests")
	}
}
