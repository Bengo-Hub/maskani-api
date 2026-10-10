// Package reminders runs the collections ladder for owing accounts and the reminders around sale
// instalments (SRDD collections, FR-24 and FR-34).
//
// One engine per customer: Maskani accounts gather many bills, owners are reached by WhatsApp with
// the estate paybill and their account number, and a debt's age is the age of the account's oldest
// unpaid bill. Treasury's per-invoice email dunning fits none of that, so it stays off for Maskani
// tenants and this ladder is the only reminder an owner gets.
package reminders

import (
	"context"
	stdsql "database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/maskaniuseroutlet"
	"github.com/bengobox/maskani-api/internal/ent/property"
	"github.com/bengobox/maskani-api/internal/ent/tenant"
	"github.com/bengobox/maskani-api/internal/ent/tenantsetting"
	"github.com/bengobox/maskani-api/internal/ent/unit"
	"github.com/bengobox/maskani-api/internal/ent/unitaccount"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/accounts"
	"github.com/bengobox/maskani-api/internal/modules/docs"
	"github.com/bengobox/maskani-api/internal/modules/register"
	"github.com/bengobox/maskani-api/internal/modules/settings"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// Service runs the ladder and the instalment reminders.
type Service struct {
	client *ent.Client
	db     *stdsql.DB
	docs   *docs.Service
	loc    *time.Location
	log    *zap.Logger
}

// NewService creates the reminders service. docs may be nil (no demand letters then).
func NewService(client *ent.Client, db *stdsql.DB, d *docs.Service, loc *time.Location, log *zap.Logger) *Service {
	if loc == nil {
		loc = time.UTC
	}
	return &Service{client: client, db: db, docs: d, loc: loc, log: log.Named("reminders")}
}

// sendHours keeps messages inside the working day (estate quiet hours are later in the evening).
const sendFrom, sendUntil = 9, 18

// InHours reports whether reminders may go out now.
func (s *Service) InHours(now time.Time) bool {
	h := now.In(s.loc).Hour()
	return h >= sendFrom && h < sendUntil
}

// Ladder is an account's place on the collections ladder, kept in its metadata under "ladder".
type Ladder struct {
	// Episode is the oldest unpaid due date the steps count from; a new one starts the ladder over.
	Episode string `json:"episode"`
	Done    []int  `json:"done,omitempty"`
	// LastDay is the day a step last ran, so an account gets at most one step a day.
	LastDay  string `json:"last_day,omitempty"`
	CallList bool   `json:"call_list,omitempty"`
	// PromiseDate pauses the demand letter and escalation until it passes.
	PromiseDate string `json:"promise_date,omitempty"`
	Notes       []Note `json:"notes,omitempty"`
}

// Note is one collections call or contact.
type Note struct {
	At          time.Time `json:"at"`
	By          string    `json:"by,omitempty"`
	Outcome     string    `json:"outcome"`
	PromiseDate string    `json:"promise_date,omitempty"`
	Text        string    `json:"text,omitempty"`
}

// ReadLadder returns an account's ladder state.
func ReadLadder(acc *ent.UnitAccount) Ladder {
	var l Ladder
	if raw, ok := acc.Metadata["ladder"]; ok {
		b, _ := json.Marshal(raw)
		_ = json.Unmarshal(b, &l)
	}
	return l
}

func (s *Service) writeLadder(ctx context.Context, acc *ent.UnitAccount, l Ladder) error {
	b, _ := json.Marshal(l)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	meta := make(map[string]any, len(acc.Metadata)+1)
	for k, v := range acc.Metadata {
		meta[k] = v
	}
	meta["ladder"] = m
	return s.client.UnitAccount.UpdateOneID(acc.ID).SetMetadata(meta).Exec(ctx)
}

// next picks the step to run for a debt of age days: the highest step reached and not yet done.
// Lower steps missed (a new estate, a long outage) are marked done without sending, so an owner
// never gets three reminders in one go. A promise to pay holds back the demand letter and
// escalation until the promised date passes.
func next(steps []settings.ArrearsStep, l Ladder, age int, today string) (settings.ArrearsStep, []int, bool) {
	done := map[int]bool{}
	for _, d := range l.Done {
		done[d] = true
	}
	var pick settings.ArrearsStep
	found := false
	marked := append([]int{}, l.Done...)
	for _, st := range steps {
		if st.Day > age || done[st.Day] {
			continue
		}
		held := l.PromiseDate != "" && l.PromiseDate >= today &&
			(st.Action == settings.ArrearsDemandLetter || st.Action == settings.ArrearsEscalate)
		if held {
			continue
		}
		pick, found = st, true
		marked = append(marked, st.Day)
	}
	return pick, marked, found
}

// owingSQL lists a tenant's owing accounts with the due date of the oldest bill the balance still
// covers (newest bills first, as treasury settles oldest first). Accounts whose balance comes from
// no issued bill (opening balances) have no due date and are skipped by the ladder.
// Parameters: $1 tenant.
const owingSQL = `
WITH acc AS (
  SELECT ua.id, ua.balance FROM unit_accounts ua
   WHERE ua.tenant_id = $1 AND ua.status = 'active' AND ua.balance > 0
), dues AS (
  SELECT l.unit_account_id AS acc_id, r.due_date, l.total AS amount
    FROM billing_run_lines l JOIN billing_runs r ON r.id = l.run_id AND r.tenant_id = $1
   WHERE l.tenant_id = $1 AND l.status = 'issued' AND l.unit_account_id IN (SELECT id FROM acc)
  UNION ALL
  SELECT sc.unit_account_id, i.due_date, i.amount
    FROM instalments i JOIN sale_contracts sc ON sc.id = i.contract_id AND sc.tenant_id = $1
   WHERE i.tenant_id = $1 AND i.treasury_invoice_id IS NOT NULL AND sc.unit_account_id IN (SELECT id FROM acc)
), running AS (
  SELECT acc_id, due_date, amount,
         SUM(amount) OVER (PARTITION BY acc_id ORDER BY due_date DESC ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS upto
    FROM dues
)
SELECT r.acc_id, MIN(r.due_date)
  FROM running r JOIN acc a ON a.id = r.acc_id
 WHERE r.upto - r.amount < a.balance
 GROUP BY r.acc_id`

type owing struct {
	ID        uuid.UUID
	OldestDue time.Time
}

func (s *Service) owing(ctx context.Context, tenantID uuid.UUID) ([]owing, error) {
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, owingSQL, tenantID)
	if err != nil {
		return nil, fmt.Errorf("owing accounts: %w", err)
	}
	defer rows.Close()
	var out []owing
	for rows.Next() {
		var o owing
		if err := rows.Scan(&o.ID, &o.OldestDue); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// RunLadder moves each owing account of each tenant one step at most, in working hours.
// Returns how many steps ran.
func (s *Service) RunLadder(ctx context.Context, tenants []uuid.UUID, now time.Time) (int, error) {
	if !s.InHours(now) {
		return 0, nil
	}
	ran := 0
	for _, tid := range tenants {
		n, err := s.runTenant(tenantguard.With(ctx, tid), tid, now)
		if err != nil {
			s.log.Warn("collections ladder failed", zap.String("tenant", tid.String()), zap.Error(err))
		}
		ran += n
	}
	return ran, nil
}

func (s *Service) runTenant(ctx context.Context, tenantID uuid.UUID, now time.Time) (int, error) {
	list, err := s.owing(ctx, tenantID)
	if err != nil || len(list) == 0 {
		return 0, err
	}
	st, _ := s.client.TenantSetting.Query().Where(tenantsetting.TenantID(tenantID)).Only(ctx)
	steps := settings.ArrearsSteps(st)
	slug := ""
	if t, err := s.client.Tenant.Query().Where(tenant.ID(tenantID)).Only(tenantguard.System(ctx)); err == nil {
		slug = t.Slug
	}
	ids := make([]uuid.UUID, len(list))
	due := make(map[uuid.UUID]time.Time, len(list))
	for i, o := range list {
		ids[i], due[o.ID] = o.ID, o.OldestDue
	}
	accs, err := s.client.UnitAccount.Query().Where(unitaccount.IDIn(ids...)).WithUnit().WithFund().All(ctx)
	if err != nil {
		return 0, err
	}
	props := s.propertyNames(ctx, accs)
	today := now.In(s.loc).Format("2006-01-02")
	ran := 0
	for _, acc := range accs {
		oldest := due[acc.ID].In(s.loc)
		age := int(now.In(s.loc).Sub(time.Date(oldest.Year(), oldest.Month(), oldest.Day(), 0, 0, 0, 0, s.loc)).Hours() / 24)
		if age < 1 {
			continue
		}
		l := ReadLadder(acc)
		episode := oldest.Format("2006-01-02")
		if l.Episode != episode {
			// Paid down to a newer bill (or a first debt): the ladder starts over; notes stay.
			l = Ladder{Episode: episode, Notes: l.Notes}
		}
		if l.LastDay == today {
			continue
		}
		step, marked, ok := next(steps, l, age, today)
		if !ok {
			continue
		}
		if err := s.act(ctx, tenantID, slug, acc, props, step, age); err != nil {
			s.log.Warn("collections step failed", zap.String("account", acc.AccountRef), zap.String("action", step.Action), zap.Error(err))
			continue
		}
		l.Done, l.LastDay = marked, today
		if step.Action == settings.ArrearsCallList {
			l.CallList = true
		}
		if err := s.writeLadder(ctx, acc, l); err != nil {
			return ran, err
		}
		ran++
	}
	return ran, nil
}

type propInfo struct {
	id   uuid.UUID
	name string
}

func (s *Service) propertyNames(ctx context.Context, accs []*ent.UnitAccount) map[uuid.UUID]propInfo {
	var ids []uuid.UUID
	for _, a := range accs {
		if a.Edges.Unit != nil {
			ids = append(ids, a.Edges.Unit.PropertyID)
		}
	}
	out := map[uuid.UUID]propInfo{}
	if len(ids) == 0 {
		return out
	}
	if ps, err := s.client.Property.Query().Where(property.IDIn(ids...)).Select(property.FieldID, property.FieldName).All(ctx); err == nil {
		for _, p := range ps {
			out[p.ID] = propInfo{p.ID, p.Name}
		}
	}
	return out
}

// act runs one step for one account.
func (s *Service) act(ctx context.Context, tenantID uuid.UUID, slug string, acc *ent.UnitAccount, props map[uuid.UUID]propInfo,
	step settings.ArrearsStep, age int) error {
	var prop propInfo
	unitCode := ""
	if acc.Edges.Unit != nil {
		prop, unitCode = props[acc.Edges.Unit.PropertyID], acc.Edges.Unit.Code
	}
	paybill := ""
	if acc.Edges.Fund != nil {
		paybill = acc.Edges.Fund.PaybillShortcode
	}
	payload := map[string]any{
		"account_id": acc.ID, "account_ref": acc.AccountRef, "name": acc.CustomerName, "phone": acc.CustomerPhone,
		"email": accounts.CustomerEmail(acc), "balance": acc.Balance.StringFixed(2), "days_overdue": age, "step_day": step.Day,
		"paybill": paybill, "unit_code": unitCode, "property_id": prop.id, "property": prop.name,
	}
	switch step.Action {
	case settings.ArrearsReminder:
		return events.Publish(ctx, s.client.OutboxEvent, tenantID, acc.ID.String(), events.ArrearsReminder, payload)
	case settings.ArrearsCallList:
		return nil // the account shows on the collections call list
	case settings.ArrearsDemandLetter:
		if s.docs == nil {
			return fmt.Errorf("documents are not set up")
		}
		k, _ := docs.DocKindOf("demand_letter")
		sub, err := s.docs.Subject(ctx, k, acc.ID)
		if err != nil {
			return err
		}
		d, err := s.docs.Issue(ctx, uuid.Nil, slug, k, acc.ID, sub, nil)
		if err != nil {
			return err
		}
		payload["document_id"], payload["document_number"] = d.ID, d.Number
		return events.Publish(ctx, s.client.OutboxEvent, tenantID, acc.ID.String(), events.ArrearsDemandLetter, payload)
	case settings.ArrearsEscalate:
		if prop.id != uuid.Nil {
			if rs, _, err := register.PropertyResponders(ctx, s.client, prop.id, []maskaniuseroutlet.PropertyRole{
				maskaniuseroutlet.PropertyRoleFinance, maskaniuseroutlet.PropertyRolePropertyManager}, 10); err == nil && len(rs) > 0 {
				payload["responders"] = rs
			}
		}
		return events.Publish(ctx, s.client.OutboxEvent, tenantID, acc.ID.String(), events.ArrearsEscalated, payload)
	}
	return fmt.Errorf("unknown action %q", step.Action)
}

// Note outcomes for a collections contact.
var noteOutcomes = map[string]bool{"reached": true, "no_answer": true, "promised": true, "disputed": true, "wrong_number": true, "paid": true}

// NoteInput records a collections contact.
type NoteInput struct {
	Outcome     string `json:"outcome"`
	PromiseDate string `json:"promise_date"`
	Text        string `json:"note"`
}

// AddNote records a call on an account; a promise to pay holds back the letter and escalation
// until its date, and "paid" or a promise takes the account off the call list.
func (s *Service) AddNote(ctx context.Context, accountID uuid.UUID, by string, in NoteInput) (*Ladder, error) {
	if !noteOutcomes[in.Outcome] {
		return nil, httpx.Invalid("outcome must be one of reached, no_answer, promised, disputed, wrong_number, paid")
	}
	if in.PromiseDate != "" {
		if _, err := time.Parse("2006-01-02", in.PromiseDate); err != nil {
			return nil, httpx.Invalid("promise_date must be YYYY-MM-DD")
		}
	}
	if in.Outcome == "promised" && in.PromiseDate == "" {
		return nil, httpx.Invalid("a promise needs its date")
	}
	acc, err := s.client.UnitAccount.Get(ctx, accountID)
	if err != nil {
		return nil, err
	}
	l := ReadLadder(acc)
	text := strings.TrimSpace(in.Text)
	if len(text) > 500 {
		text = text[:500]
	}
	l.Notes = append(l.Notes, Note{At: time.Now(), By: by, Outcome: in.Outcome, PromiseDate: in.PromiseDate, Text: text})
	if len(l.Notes) > 20 {
		l.Notes = l.Notes[len(l.Notes)-20:]
	}
	if in.PromiseDate != "" {
		l.PromiseDate = in.PromiseDate
	}
	if in.Outcome == "promised" || in.Outcome == "paid" {
		l.CallList = false
	}
	if err := s.writeLadder(ctx, acc, l); err != nil {
		return nil, err
	}
	return &l, nil
}

// CallRow is one account on the collections call list.
type CallRow struct {
	AccountID   uuid.UUID       `json:"account_id"`
	AccountRef  string          `json:"account_ref"`
	Customer    string          `json:"customer_name"`
	Phone       string          `json:"customer_phone"`
	UnitCode    string          `json:"unit_code"`
	PropertyID  uuid.UUID       `json:"property_id"`
	Balance     decimal.Decimal `json:"balance"`
	LastPayment *time.Time      `json:"last_payment_at,omitempty"`
	Since       string          `json:"oldest_due"`
	PromiseDate string          `json:"promise_date,omitempty"`
	LastNote    *Note           `json:"last_note,omitempty"`
}

// CallList lists the accounts the ladder put on the call list that still owe, largest first, in
// the given properties (nil means all).
func (s *Service) CallList(ctx context.Context, propertyIDs []uuid.UUID, all bool) ([]CallRow, error) {
	q := s.client.UnitAccount.Query().Where(unitaccount.BalanceGT(decimal.Zero), unitaccount.StatusEQ(unitaccount.StatusActive),
		func(sel *entsql.Selector) {
			sel.Where(sqljson.ValueEQ(unitaccount.FieldMetadata, true, sqljson.Path("ladder", "call_list")))
		})
	if !all {
		q = q.Where(unitaccount.HasUnitWith(unit.PropertyIDIn(propertyIDs...)))
	}
	accs, err := q.WithUnit().Order(ent.Desc(unitaccount.FieldBalance)).Limit(300).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]CallRow, 0, len(accs))
	for _, a := range accs {
		l := ReadLadder(a)
		row := CallRow{AccountID: a.ID, AccountRef: a.AccountRef, Customer: a.CustomerName, Phone: a.CustomerPhone,
			Balance: a.Balance, LastPayment: a.LastPaymentAt, Since: l.Episode, PromiseDate: l.PromiseDate}
		if a.Edges.Unit != nil {
			row.UnitCode, row.PropertyID = a.Edges.Unit.Code, a.Edges.Unit.PropertyID
		}
		if n := len(l.Notes); n > 0 {
			row.LastNote = &l.Notes[n-1]
		}
		out = append(out, row)
	}
	return out, nil
}

// AccountLadder returns an account's place on the collections ladder.
func (s *Service) AccountLadder(ctx context.Context, accountID uuid.UUID) (*Ladder, error) {
	acc, err := s.client.UnitAccount.Get(ctx, accountID)
	if err != nil {
		return nil, err
	}
	l := ReadLadder(acc)
	return &l, nil
}
