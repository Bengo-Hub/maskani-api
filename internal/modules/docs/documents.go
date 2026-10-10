package docs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
	"github.com/Bengo-Hub/reports"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/document"
	"github.com/bengobox/maskani-api/internal/ent/party"
	"github.com/bengobox/maskani-api/internal/ent/predicate"
	"github.com/bengobox/maskani-api/internal/ent/property"
	"github.com/bengobox/maskani-api/internal/ent/salecontract"
	"github.com/bengobox/maskani-api/internal/ent/unit"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/accounts"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// Storage is where issued documents are kept and how they are verified.
type Storage struct {
	Root      string // media root; files go under tenants/{tenant}/documents/{yyyy-mm}/
	VerifyURL string // public page, the code is appended: https://.../verify/
}

// Numberer allocates document numbers (the sequence allocator).
type Numberer interface {
	Next(ctx context.Context, kind, prefix string) (string, error)
}

// SetIssuing enables issuing documents; without it Issue refuses.
func (s *Service) SetIssuing(st Storage, num Numberer) { s.store, s.num = st, num }

// IssueInput asks for a document from the template in use.
type IssueInput struct {
	Kind     string            `json:"kind"`
	EntityID uuid.UUID         `json:"entity_id"`
	Values   map[string]string `json:"values"` // the kind's inputs (payment plan terms)
}

// Subject is what a document is about, resolved before issuing so the handler can check the
// caller's property scope first.
type Subject struct {
	PropertyID uuid.UUID
	UnitID     uuid.UUID
	PartyIDs   []string
	values     map[string]string
}

// Subject loads what a document is about and its merge values; callers authorise PropertyID
// before issuing.
func (s *Service) Subject(ctx context.Context, k DocKind, entityID uuid.UUID) (*Subject, error) {
	switch k.EntityType {
	case "unit_account":
		return s.accountSubject(ctx, k, entityID)
	case "sale_contract":
		return s.contractSubject(ctx, entityID)
	}
	return nil, httpx.Invalid("unknown document subject")
}

func (s *Service) accountSubject(ctx context.Context, k DocKind, id uuid.UUID) (*Subject, error) {
	st, err := s.collections.Statement(ctx, id) // refreshes the balance from treasury
	if err != nil {
		return nil, err
	}
	if st.Ledger == nil {
		return nil, httpx.Unavailable("the accounts service is not answering; try again in a minute")
	}
	acc := st.Account
	u := acc.Edges.Unit
	if u == nil {
		return nil, httpx.Invalid("the account has no unit")
	}
	due := st.Ledger.Balance.Sub(st.Ledger.Credit)
	if k.Code == "clearance_certificate" && due.IsPositive() {
		return nil, httpx.Conflict(fmt.Sprintf("the account still owes %s, so it cannot be cleared", moneyText("KES", due)))
	}
	if k.Code == "demand_letter" && !due.IsPositive() {
		return nil, httpx.Conflict("the account owes nothing")
	}
	v := map[string]string{"unit_code": u.Code, "account_ref": acc.AccountRef, "owner_name": acc.CustomerName,
		"balance": moneyText("KES", decimal.Max(due, decimal.Zero)), "last_paid": "no payment yet"}
	if st.Ledger.LastPaidAt != nil {
		v["last_paid"] = reports.Date(st.Ledger.LastPaidAt.In(s.loc))
	}
	if f := acc.Edges.Fund; f != nil {
		v["fund_name"], v["paybill"], v["pay_account"] = f.Name, f.PaybillShortcode, accounts.PayInstructionFor(f, acc.AccountRef).Account
	}
	sub := &Subject{PropertyID: u.PropertyID, UnitID: u.ID, values: v}
	if acc.PrimaryPartyID != nil {
		sub.PartyIDs = []string{acc.PrimaryPartyID.String()}
	}
	return sub, nil
}

func (s *Service) contractSubject(ctx context.Context, id uuid.UUID) (*Subject, error) {
	c, err := s.client.SaleContract.Query().Where(salecontract.ID(id)).Only(ctx)
	if err != nil {
		return nil, err
	}
	u, err := s.client.Unit.Query().Where(unit.ID(c.UnitID)).Select(unit.FieldCode).Only(ctx)
	if err != nil {
		return nil, err
	}
	buyer, err := s.client.Party.Query().Where(party.ID(c.PrimaryBuyerID)).Only(ctx)
	if err != nil {
		return nil, err
	}
	v := map[string]string{"unit_code": u.Code, "contract_number": c.ContractNumber, "buyer_name": partyName(buyer),
		"price": moneyText("KES", c.NetPrice), "deposit": moneyText("KES", c.DepositAmount),
		"term_months": strconv.Itoa(c.TermMonths), "payment_option": strings.ReplaceAll(string(c.PaymentOption), "_", " "),
		"balance": moneyText("KES", c.NetPrice.Sub(c.PaidTotal))}
	return &Subject{PropertyID: c.PropertyID, UnitID: c.UnitID, PartyIDs: []string{c.PrimaryBuyerID.String()}, values: v}, nil
}

func partyName(p *ent.Party) string {
	if p.DisplayName != "" {
		return p.DisplayName
	}
	if p.CompanyName != "" {
		return p.CompanyName
	}
	return strings.TrimSpace(p.FirstName + " " + p.LastName)
}

// Issue renders the document from the template in use, numbers it, stores the PDF with its hash
// and a verification code, and records it as issued. sub comes from Subject, already authorised.
func (s *Service) Issue(ctx context.Context, actor uuid.UUID, slug string, k DocKind, entityID uuid.UUID, sub *Subject, inputs map[string]string) (*ent.Document, error) {
	if s.num == nil || s.store.Root == "" {
		return nil, httpx.Unavailable("document storage is not set up")
	}
	tenantID, err := tenantguard.MustTenant(ctx)
	if err != nil {
		return nil, err
	}
	for _, f := range k.Inputs {
		if strings.TrimSpace(inputs[f]) == "" {
			return nil, httpx.Invalid(strings.ReplaceAll(f, "_", " ") + " is required")
		}
	}
	tpl, err := s.inUse(ctx, tenantID, k)
	if err != nil {
		return nil, err
	}
	number, err := s.num.Next(ctx, "document", "DOC")
	if err != nil {
		return nil, err
	}
	code, err := verificationCode()
	if err != nil {
		return nil, err
	}

	now := time.Now().In(s.loc)
	r := &reports.Report{Title: k.Name, Subtitle: number, GeneratedAt: now, Currency: "KES"}
	s.brand.Apply(ctx, tenantID, slug, r)
	values := map[string]string{"estate_name": r.TenantName, "date": reports.Date(now), "document_number": number}
	if p, err := s.client.Property.Query().Where(property.ID(sub.PropertyID)).Select(property.FieldName).Only(ctx); err == nil {
		r.OutletName, values["property_name"] = p.Name, p.Name
	}
	for key, val := range sub.values {
		values[key] = val
	}
	for _, f := range k.Inputs {
		values[f] = strings.TrimSpace(inputs[f])
	}
	r.Meta = append([][2]string{{"Document", number}, {"Verification code", code}}, r.Meta...)
	r.Footer = fmt.Sprintf("%s · check it is genuine at %s%s", number, s.store.VerifyURL, code)
	r.Sections = []reports.Section{
		{Kind: reports.SectionProse, Paragraphs: paragraphs(merge(tpl.Body, values))},
		{Kind: reports.SectionSignature, Signatures: []reports.Signature{
			{Role: "For and on behalf of " + r.TenantName, DateLabel: "Date"},
			{Role: "Received by", Name: firstNonEmpty(values["owner_name"], values["buyer_name"]), DateLabel: "Date"},
		}},
	}
	body, _, err := reports.Generate(r, reports.FormatPDF)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	key := filepath.ToSlash(filepath.Join("tenants", tenantID.String(), "documents", now.Format("2006-01"), uuid.New().String()+".pdf"))
	if err := writeFile(filepath.Join(s.store.Root, filepath.FromSlash(key)), body); err != nil {
		return nil, fmt.Errorf("store document: %w", err)
	}

	c := s.client.Document.Create().SetNumber(number).SetKind(k.Code).SetTitle(k.Name).SetEntityType(k.EntityType).
		SetEntityID(entityID).SetUnitID(sub.UnitID).SetPartyIds(sub.PartyIDs).SetFileKey(key).SetSha256(hex.EncodeToString(sum[:])).
		SetVerificationCode(code).SetStatus(document.StatusIssued).SetIssuedAt(now).SetCreatedBy(actor).
		SetMetadata(map[string]any{"property_id": sub.PropertyID.String(), "template_version": tpl.Version})
	if tpl.ID != nil {
		c.SetTemplateID(*tpl.ID).SetTemplateVersion(tpl.Version)
	}
	return c.Save(ctx)
}

// Documents lists an entity's documents, newest first.
func (s *Service) Documents(ctx context.Context, entityType string, entityID uuid.UUID) ([]*ent.Document, error) {
	return s.client.Document.Query().Where(document.EntityType(entityType), document.EntityID(entityID)).
		Order(ent.Desc(document.FieldCreatedAt)).Limit(200).All(ctx)
}

// UnitDocuments lists every document about a unit (its accounts and contracts), newest first.
func (s *Service) UnitDocuments(ctx context.Context, unitID uuid.UUID) ([]*ent.Document, error) {
	return s.client.Document.Query().Where(document.UnitID(unitID)).Order(ent.Desc(document.FieldCreatedAt)).Limit(200).All(ctx)
}

// PartyDocuments lists the documents addressed to any of these parties (the portal), newest first.
func (s *Service) PartyDocuments(ctx context.Context, partyIDs []uuid.UUID) ([]*ent.Document, error) {
	if len(partyIDs) == 0 {
		return []*ent.Document{}, nil
	}
	anyParty := make([]predicate.Document, len(partyIDs))
	for i, id := range partyIDs {
		v := id.String()
		anyParty[i] = func(sel *entsql.Selector) { sel.Where(sqljson.ValueContains(document.FieldPartyIds, v)) }
	}
	return s.client.Document.Query().Where(document.StatusNEQ(document.StatusDraft), document.Or(anyParty...)).
		Order(ent.Desc(document.FieldCreatedAt)).Limit(200).All(ctx)
}

// Document loads one document.
func (s *Service) Document(ctx context.Context, id uuid.UUID) (*ent.Document, error) {
	return s.client.Document.Get(ctx, id)
}

// PropertyOf returns the property a document belongs to (for scope checks).
func PropertyOf(d *ent.Document) uuid.UUID {
	if v, ok := d.Metadata["property_id"].(string); ok {
		if id, err := uuid.Parse(v); err == nil {
			return id
		}
	}
	return uuid.Nil
}

// File reads a document's PDF and records who downloaded it.
func (s *Service) File(ctx context.Context, d *ent.Document, actor *uuid.UUID, actorKind, ip string) (*File, error) {
	if d.FileKey == "" || s.store.Root == "" {
		return nil, httpx.Unavailable("the document file is not available")
	}
	body, err := os.ReadFile(filepath.Join(s.store.Root, filepath.FromSlash(d.FileKey)))
	if err != nil {
		return nil, fmt.Errorf("read document: %w", err)
	}
	if _, err := s.client.DocumentAccessLog.Create().SetDocumentID(d.ID).SetNillableActorID(actor).
		SetActorKind(actorKind).SetAction("download").SetIP(ip).Save(ctx); err != nil {
		return nil, err
	}
	return &File{Body: body, Mime: reports.MimePDF, Name: fileSafe(d.Number) + ".pdf"}, nil
}

// Verification is what anyone holding a document may confirm: no personal data.
type Verification struct {
	Number   string     `json:"number"`
	Title    string     `json:"title"`
	Status   string     `json:"status"`
	IssuedAt *time.Time `json:"issued_at,omitempty"`
	Issuer   string     `json:"issuer"`
	SHA256   string     `json:"sha256"`
}

// Verify looks a document up by its printed code (public, any tenant).
func (s *Service) Verify(ctx context.Context, code string) (*Verification, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) != codeLen {
		return nil, &ent.NotFoundError{}
	}
	sys := tenantguard.System(ctx)
	d, err := s.client.Document.Query().Where(document.VerificationCode(code), document.StatusNEQ(document.StatusDraft)).Only(sys)
	if err != nil {
		return nil, err
	}
	issuer := ""
	if p, err := s.client.Property.Query().Where(property.ID(PropertyOf(d))).Select(property.FieldName).Only(sys); err == nil {
		issuer = p.Name
	}
	return &Verification{Number: d.Number, Title: d.Title, Status: string(d.Status), IssuedAt: d.IssuedAt, Issuer: issuer, SHA256: d.Sha256}, nil
}

// codeLen and codeAlphabet: 10 characters without look-alikes (0/O, 1/I/L), about 49 bits.
const (
	codeLen      = 10
	codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
)

// verificationCode draws each character uniformly: bytes at or above the largest multiple of the
// alphabet size are thrown away, so no character is likelier than another.
func verificationCode() (string, error) {
	const limit = 256 - 256%len(codeAlphabet)
	out := make([]byte, 0, codeLen)
	buf := make([]byte, 2*codeLen)
	for len(out) < codeLen {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if int(b) < limit && len(out) < codeLen {
				out = append(out, codeAlphabet[int(b)%len(codeAlphabet)])
			}
		}
	}
	return string(out), nil
}

func writeFile(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o644)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
