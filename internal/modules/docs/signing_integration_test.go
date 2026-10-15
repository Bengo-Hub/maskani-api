package docs

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/config"
	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/outboxevent"
	_ "github.com/bengobox/maskani-api/internal/ent/runtime"
	"github.com/bengobox/maskani-api/internal/platform/database"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
	"github.com/bengobox/maskani-api/internal/shared/otp"
	"github.com/bengobox/maskani-api/internal/shared/secure"
)

// TestSigningOnPostgres: a notice cannot be signed; each person named on an agreement accepts it by
// reading back a code sent to their record (wrong codes refused, nobody else may accept), the
// document is partly signed then executed with the hash on every signature and one
// document.executed event; a signed paper copy executes another agreement at once. Needs
// DOCS_TEST_POSTGRES_URL (a scratch database).
func TestSigningOnPostgres(t *testing.T) {
	dsn := os.Getenv("DOCS_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("DOCS_TEST_POSTGRES_URL not set")
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
	tenant := uuid.New()
	ctx := tenantguard.With(context.Background(), tenant)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	box, err := secure.NewBox("test-secret-key-for-document-signing-01")
	must(err)
	s := &Service{client: client, loc: time.UTC}
	s.SetSigning(box)
	a, err := client.Party.Create().SetDisplayName("Owner A").SetPhone("254712000444").Save(ctx)
	must(err)
	b, err := client.Party.Create().SetDisplayName("Joint owner B").SetEmail("b@example.com").Save(ctx)
	must(err)
	stranger := uuid.New()
	doc := func(kind string, parties ...uuid.UUID) *ent.Document {
		ids := make([]string, len(parties))
		for i, p := range parties {
			ids[i] = p.String()
		}
		d, err := client.Document.Create().SetNumber("DOC-" + uuid.NewString()[:6]).SetKind(kind).SetTitle(kind).
			SetEntityType("unit_account").SetEntityID(uuid.New()).SetPartyIds(ids).SetSha256("abc123").
			SetVerificationCode(uuid.NewString()[:10]).SetStatus("issued").Save(ctx)
		must(err)
		return d
	}
	lastCode := func() string {
		t.Helper()
		ev, err := client.OutboxEvent.Query().Where(outboxevent.EventType(otp.Requested)).Order(ent.Desc(outboxevent.FieldCreatedAt)).First(ctx)
		must(err)
		var env struct {
			Payload map[string]any `json:"payload"`
		}
		must(json.Unmarshal(ev.Payload, &env))
		if env.Payload["purpose"] != "document" {
			t.Fatalf("code purpose: %v", env.Payload["purpose"])
		}
		return env.Payload["otp"].(string)
	}

	notice := doc("demand_letter", a.ID)
	if err := s.SendAcceptanceCode(ctx, notice.ID, a.ID); err == nil {
		t.Fatal("a notice is not signed")
	}
	plan := doc("payment_plan", a.ID, b.ID)
	if err := s.SendAcceptanceCode(ctx, plan.ID, stranger); err == nil {
		t.Fatal("only people named on the document")
	}
	must(s.SendAcceptanceCode(ctx, plan.ID, a.ID))
	codeA := lastCode()
	if _, err := s.Accept(ctx, plan.ID, Acceptance{PartyID: a.ID, Code: "000000", Via: "portal"}); err == nil {
		t.Fatal("a wrong code is refused")
	}
	got, err := s.Accept(ctx, plan.ID, Acceptance{PartyID: a.ID, Code: codeA, Via: "portal", IP: "10.0.0.9"})
	must(err)
	if got.Status != "partly_signed" || got.Metadata["acceptance_codes"] != nil {
		t.Fatalf("after A: %s, codes hidden %v", got.Status, got.Metadata["acceptance_codes"])
	}
	if err := s.SendAcceptanceCode(ctx, plan.ID, a.ID); err == nil {
		t.Fatal("A has already accepted")
	}
	must(s.SendAcceptanceCode(ctx, plan.ID, b.ID))
	got, err = s.Accept(ctx, plan.ID, Acceptance{PartyID: b.ID, Code: lastCode(), Via: "office"})
	must(err)
	if got.Status != "executed" || got.ExecutedAt == nil {
		t.Fatalf("after B: %s", got.Status)
	}
	sigs, err := s.Signatures(ctx, plan.ID)
	must(err)
	if len(sigs) != 2 || sigs[0].DocumentHash != "abc123" || sigs[0].IP != "10.0.0.9" {
		t.Fatalf("signatures: %+v", sigs)
	}
	if n, _ := client.OutboxEvent.Query().Where(outboxevent.EventType(DocumentExecuted)).Count(ctx); n != 1 {
		t.Fatalf("executed events: %d", n)
	}

	offer := doc("offer_letter", a.ID)
	if _, err := s.RecordWetSigned(ctx, offer.ID, stranger, []string{"elsewhere/x.jpg"}); err == nil {
		t.Fatal("scans must be the tenant's document uploads")
	}
	got, err = s.RecordWetSigned(ctx, offer.ID, stranger, []string{"tenants/" + tenant.String() + "/documents/2026-10/p1.jpg"})
	must(err)
	if got.Status != "executed" {
		t.Fatalf("signed copy executes: %s", got.Status)
	}
}
