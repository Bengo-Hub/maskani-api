package docs

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/documenttemplate"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// A template body is plain text: paragraphs separated by a blank line, a paragraph starting "**"
// is a bold clause lead-in, and {{field}} is replaced when the document is issued. Plain text (not
// HTML) because the report engine lays out paragraphs itself and nothing else needs sanitising.

// DocKind is a kind of issued document: what it is about and the fields its templates may use.
type DocKind struct {
	Code       string   `json:"code"`
	Name       string   `json:"name"`
	EntityType string   `json:"entity_type"` // unit_account or sale_contract
	Fields     []string `json:"merge_fields"`
	// Inputs are fields the issuer types in when issuing (not read from records).
	Inputs []string `json:"inputs,omitempty"`
}

var accountFields = []string{"estate_name", "property_name", "unit_code", "account_ref", "owner_name", "fund_name",
	"paybill", "pay_account", "balance", "last_paid", "date", "document_number"}

var contractFields = []string{"estate_name", "property_name", "unit_code", "contract_number", "buyer_name", "price",
	"deposit", "term_months", "payment_option", "balance", "date", "document_number"}

// DocKinds are the documents an estate can issue, in screen order.
var DocKinds = []DocKind{
	{Code: "clearance_certificate", Name: "Clearance certificate", EntityType: "unit_account", Fields: accountFields},
	{Code: "demand_letter", Name: "Demand letter", EntityType: "unit_account", Fields: accountFields},
	{Code: "payment_plan", Name: "Payment plan agreement", EntityType: "unit_account",
		Fields: append(append([]string{}, accountFields...), "instalments", "instalment_amount", "first_due"),
		Inputs: []string{"instalments", "instalment_amount", "first_due"}},
	{Code: "offer_letter", Name: "Letter of offer", EntityType: "sale_contract", Fields: contractFields},
}

// DocKindOf returns a document kind by code.
func DocKindOf(code string) (DocKind, bool) {
	for _, k := range DocKinds {
		if k.Code == code {
			return k, true
		}
	}
	return DocKind{}, false
}

// starterBodies are the Codevertex templates every estate starts from until it approves its own.
var starterBodies = map[string]string{
	"clearance_certificate": `This is to certify that account {{account_ref}} for unit {{unit_code}} at {{property_name}}, held by {{owner_name}}, has no amount owing to {{estate_name}} ({{fund_name}}) as at {{date}}.

This certificate covers charges billed up to {{date}}. Charges billed after that date are not covered.`,
	"demand_letter": `Dear {{owner_name}},

**Amount owing on unit {{unit_code}}
Our records show {{balance}} owing on account {{account_ref}} at {{property_name}}. The last payment we received was on {{last_paid}}.

Please pay the full amount within 14 days of this letter to paybill {{paybill}}, account {{account_ref}}. If you have already paid, or you would like to agree a payment plan, please contact the estate office.

If the amount is not paid or a plan agreed, the estate may take further steps as its rules allow.`,
	"payment_plan": `This agreement is between {{estate_name}} and {{owner_name}} for account {{account_ref}}, unit {{unit_code}} at {{property_name}}.

**Amount covered
{{balance}} owing as at {{date}}.

**Instalments
{{instalments}} instalments of {{instalment_amount}}, the first due on {{first_due}} and then monthly, paid to paybill {{paybill}}, account {{account_ref}}.

**Current charges
New charges billed while the plan runs are paid as they fall due, on top of the instalments.

**If an instalment is missed
If an instalment is more than 14 days late, the plan ends and the full balance becomes due at once.`,
	"offer_letter": `Dear {{buyer_name}},

**Unit {{unit_code}} at {{property_name}}
{{estate_name}} offers to sell you unit {{unit_code}} for {{price}} under contract {{contract_number}}, payment option: {{payment_option}}.

**Deposit and payments
A deposit of {{deposit}} is due on signing. The balance is payable over {{term_months}} months as set out in your payment schedule.

**Acceptance
This offer is open for 14 days from {{date}}. Please confirm your acceptance by signing below or accepting in the owner portal.`,
}

var fieldRe = regexp.MustCompile(`\{\{\s*([a-z_]+)\s*\}\}`)

// Template is a template version as the screens show it.
type Template struct {
	Code       string     `json:"code"`
	Name       string     `json:"name"`
	EntityType string     `json:"entity_type"`
	Version    int        `json:"version"` // 0 is the Codevertex starter
	Status     string     `json:"status"`  // draft, approved, retired
	Body       string     `json:"body"`
	Fields     []string   `json:"merge_fields"`
	Inputs     []string   `json:"inputs,omitempty"`
	ApprovedAt *time.Time `json:"approved_at,omitempty"`
	ID         *uuid.UUID `json:"id,omitempty"`
}

func starter(k DocKind) Template {
	return Template{Code: k.Code, Name: k.Name, EntityType: k.EntityType, Status: "approved", Body: starterBodies[k.Code],
		Fields: k.Fields, Inputs: k.Inputs}
}

func fromRow(k DocKind, t *ent.DocumentTemplate) Template {
	id := t.ID
	return Template{Code: k.Code, Name: t.Name, EntityType: k.EntityType, Version: t.Version, Status: string(t.Status),
		Body: t.Body, Fields: k.Fields, Inputs: k.Inputs, ApprovedAt: t.ApprovedAt, ID: &id}
}

// Templates lists, per kind, the template in use (the tenant's approved version, else the starter)
// and the latest draft when there is one.
func (s *Service) Templates(ctx context.Context) ([]Template, error) {
	tenantID, err := tenantguard.MustTenant(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.client.DocumentTemplate.Query().Where(documenttemplate.TenantID(tenantID),
		documenttemplate.StatusIn(documenttemplate.StatusApproved, documenttemplate.StatusDraft)).
		Order(ent.Desc(documenttemplate.FieldVersion)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := []Template{}
	for _, k := range DocKinds {
		inUse, draft := starter(k), (*Template)(nil)
		for _, r := range rows {
			if r.Code != k.Code {
				continue
			}
			if r.Status == documenttemplate.StatusApproved && inUse.Version == 0 {
				inUse = fromRow(k, r)
			}
			if r.Status == documenttemplate.StatusDraft && draft == nil {
				d := fromRow(k, r)
				draft = &d
			}
		}
		out = append(out, inUse)
		if draft != nil {
			out = append(out, *draft)
		}
	}
	return out, nil
}

// inUse is the template a new document of the kind is issued from.
func (s *Service) inUse(ctx context.Context, tenantID uuid.UUID, k DocKind) (Template, error) {
	row, err := s.client.DocumentTemplate.Query().Where(documenttemplate.TenantID(tenantID), documenttemplate.Code(k.Code),
		documenttemplate.StatusEQ(documenttemplate.StatusApproved)).Order(ent.Desc(documenttemplate.FieldVersion)).First(ctx)
	if ent.IsNotFound(err) {
		return starter(k), nil
	}
	if err != nil {
		return Template{}, err
	}
	return fromRow(k, row), nil
}

// TemplateInput is a new draft wording.
type TemplateInput struct {
	Name string `json:"name"`
	Body string `json:"body"`
}

// SaveDraft stores new wording for a kind as a draft, replacing an earlier draft, so the approved
// version stays in use until someone with documents.manage approves this one.
func (s *Service) SaveDraft(ctx context.Context, code string, in TemplateInput) (*Template, error) {
	k, ok := DocKindOf(code)
	if !ok {
		return nil, httpx.Invalid("unknown document kind")
	}
	body := strings.TrimSpace(strings.ReplaceAll(in.Body, "\r\n", "\n"))
	if body == "" {
		return nil, httpx.Invalid("the wording is empty")
	}
	if len(body) > 20000 {
		return nil, httpx.Invalid("the wording is too long (20,000 characters at most)")
	}
	if unknown := unknownFields(body, k.Fields); len(unknown) > 0 {
		return nil, httpx.Invalid("unknown fields: {{" + strings.Join(unknown, "}}, {{") + "}}")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = k.Name
	}
	tenantID, err := tenantguard.MustTenant(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.DocumentTemplate.Delete().Where(documenttemplate.TenantID(tenantID), documenttemplate.Code(code),
		documenttemplate.StatusEQ(documenttemplate.StatusDraft)).Exec(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	latest, err := tx.DocumentTemplate.Query().Where(documenttemplate.TenantID(tenantID), documenttemplate.Code(code)).
		Aggregate(ent.Max(documenttemplate.FieldVersion)).Int(ctx)
	if err != nil {
		latest = 0 // no versions yet
	}
	row, err := tx.DocumentTemplate.Create().SetTenantID(tenantID).SetCode(code).SetName(name).SetCategory(k.EntityType).
		SetVersion(latest + 1).SetBody(body).SetMergeFields(usedFields(body)).SetStatus(documenttemplate.StatusDraft).Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	t := fromRow(k, row)
	return &t, nil
}

// Approve puts a draft into use and retires the version it replaces.
func (s *Service) Approve(ctx context.Context, actor uuid.UUID, code string, version int) (*Template, error) {
	k, ok := DocKindOf(code)
	if !ok {
		return nil, httpx.Invalid("unknown document kind")
	}
	tenantID, err := tenantguard.MustTenant(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	row, err := tx.DocumentTemplate.Query().Where(documenttemplate.TenantID(tenantID), documenttemplate.Code(code),
		documenttemplate.Version(version), documenttemplate.StatusEQ(documenttemplate.StatusDraft)).Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			return nil, httpx.Conflict("that version is not a draft waiting for approval")
		}
		return nil, err
	}
	if _, err := tx.DocumentTemplate.Update().Where(documenttemplate.TenantID(tenantID), documenttemplate.Code(code),
		documenttemplate.StatusEQ(documenttemplate.StatusApproved)).SetStatus(documenttemplate.StatusRetired).Save(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	now := time.Now()
	row, err = tx.DocumentTemplate.UpdateOne(row).SetStatus(documenttemplate.StatusApproved).SetApprovedBy(actor).SetApprovedAt(now).Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	t := fromRow(k, row)
	return &t, nil
}

func usedFields(body string) []string {
	seen := map[string]bool{}
	for _, m := range fieldRe.FindAllStringSubmatch(body, -1) {
		seen[m[1]] = true
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

func unknownFields(body string, allowed []string) []string {
	ok := map[string]bool{}
	for _, f := range allowed {
		ok[f] = true
	}
	var out []string
	for _, f := range usedFields(body) {
		if !ok[f] {
			out = append(out, f)
		}
	}
	return out
}

// merge fills {{field}} from values; a field without a value prints as a blank line to fill by hand.
func merge(body string, values map[string]string) string {
	return fieldRe.ReplaceAllStringFunc(body, func(m string) string {
		f := fieldRe.FindStringSubmatch(m)[1]
		if v := strings.TrimSpace(values[f]); v != "" {
			return v
		}
		return "__________"
	})
}

// paragraphs splits merged wording into the report's prose paragraphs. A bold lead-in line keeps
// its own paragraph and the text under it follows as the next one.
func paragraphs(text string) []string {
	var out []string
	for _, block := range strings.Split(text, "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		if strings.HasPrefix(block, "**") {
			head, rest, _ := strings.Cut(block, "\n")
			out = append(out, head)
			if rest = strings.TrimSpace(rest); rest != "" {
				out = append(out, strings.Join(strings.Fields(rest), " "))
			}
			continue
		}
		out = append(out, strings.Join(strings.Fields(block), " "))
	}
	return out
}
