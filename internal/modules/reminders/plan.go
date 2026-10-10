package reminders

import (
	"context"
	"sort"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/accountcollection"
	"github.com/bengobox/maskani-api/internal/ent/maskaniuseroutlet"
	"github.com/bengobox/maskani-api/internal/ent/unitaccount"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/register"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// PaymentPlanBroken tells finance and the manager that an agreed plan fell behind.
const PaymentPlanBroken = "payment_plan.broken"

// Plan statuses.
const (
	PlanActive    = "active"
	PlanCompleted = "completed"
	PlanBroken    = "broken"
	PlanCancelled = "cancelled"
)

// planGraceDays is how late an agreed instalment may be before the plan counts as broken.
const planGraceDays = 3

// PlanInstalment is one agreed payment.
type PlanInstalment struct {
	Due    string          `json:"due"` // YYYY-MM-DD
	Amount decimal.Decimal `json:"amount"`
}

// Plan is a payment plan agreed with an owner for their arrears, kept on the ladder. While active
// it holds back the demand letter and escalation. Paid counts the account's payments from Start
// (account_collections), so new bills do not hide progress.
type Plan struct {
	Status      string           `json:"status"`
	Start       string           `json:"start"`
	By          string           `json:"by,omitempty"`
	Note        string           `json:"note,omitempty"`
	Instalments []PlanInstalment `json:"instalments"`
	Total       decimal.Decimal  `json:"total"`
	Paid        decimal.Decimal  `json:"paid"`
	Checked     string           `json:"checked,omitempty"`
	ClosedOn    string           `json:"closed_on,omitempty"`
}

// PlanInput is the schedule staff agree with the owner.
type PlanInput struct {
	Instalments []PlanInstalment `json:"instalments"`
	Note        string           `json:"note"`
}

func (p *Plan) active() bool { return p != nil && p.Status == PlanActive }

// SetPlan agrees a payment plan on an owing account (replacing an active one). Instalments are
// 1 to 24, dated from today in order, each above zero, and together no more than the balance.
func (s *Service) SetPlan(ctx context.Context, accountID uuid.UUID, by string, in PlanInput) (*Ladder, error) {
	n := len(in.Instalments)
	if n == 0 || n > 24 {
		return nil, httpx.Invalid("a plan has from 1 to 24 instalments")
	}
	today := time.Now().In(s.loc).Format("2006-01-02")
	sort.SliceStable(in.Instalments, func(i, j int) bool { return in.Instalments[i].Due < in.Instalments[j].Due })
	total := decimal.Zero
	for i, it := range in.Instalments {
		if _, err := time.Parse("2006-01-02", it.Due); err != nil {
			return nil, httpx.Invalid("instalment dates must be YYYY-MM-DD")
		}
		if it.Due < today {
			return nil, httpx.Invalid("instalment dates must be today or later")
		}
		if i > 0 && it.Due == in.Instalments[i-1].Due {
			return nil, httpx.Invalid("two instalments share a date")
		}
		if !it.Amount.IsPositive() {
			return nil, httpx.Invalid("each instalment needs an amount above zero")
		}
		total = total.Add(it.Amount.Round(2))
	}
	acc, err := s.client.UnitAccount.Get(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if !acc.Balance.IsPositive() {
		return nil, httpx.Invalid("this account owes nothing")
	}
	if total.GreaterThan(acc.Balance) {
		return nil, httpx.Invalid("the plan adds up to " + total.StringFixed(2) + ", more than the " + acc.Balance.StringFixed(2) + " owed")
	}
	note := in.Note
	if len(note) > 500 {
		note = note[:500]
	}
	l := ReadLadder(acc)
	l.Plan = &Plan{Status: PlanActive, Start: today, By: by, Note: note, Instalments: in.Instalments, Total: total, Paid: decimal.Zero}
	l.CallList = false // agreed: off the call list
	l.Notes = append(l.Notes, Note{At: time.Now(), By: by, Outcome: "plan", Text: "Payment plan of " + total.StringFixed(2) + " in " + itoa(n) + " instalments"})
	if len(l.Notes) > 20 {
		l.Notes = l.Notes[len(l.Notes)-20:]
	}
	if err := s.writeLadder(ctx, acc, l); err != nil {
		return nil, err
	}
	return &l, nil
}

// CancelPlan ends an active plan; the ladder carries on from where the debt is.
func (s *Service) CancelPlan(ctx context.Context, accountID uuid.UUID, by string) (*Ladder, error) {
	acc, err := s.client.UnitAccount.Get(ctx, accountID)
	if err != nil {
		return nil, err
	}
	l := ReadLadder(acc)
	if !l.Plan.active() {
		return nil, httpx.Conflict("this account has no active plan")
	}
	l.Plan.Status, l.Plan.ClosedOn = PlanCancelled, time.Now().In(s.loc).Format("2006-01-02")
	l.Notes = append(l.Notes, Note{At: time.Now(), By: by, Outcome: "plan", Text: "Payment plan cancelled"})
	if err := s.writeLadder(ctx, acc, l); err != nil {
		return nil, err
	}
	return &l, nil
}

// planState works out a plan's paid amount and status: completed once the agreed total is paid,
// broken once what was due (past the grace days) is more than what was paid.
func planState(p Plan, paid decimal.Decimal, today time.Time) string {
	if paid.GreaterThanOrEqual(p.Total) {
		return PlanCompleted
	}
	dueBy := decimal.Zero
	cutoff := today.AddDate(0, 0, -planGraceDays).Format("2006-01-02")
	for _, it := range p.Instalments {
		if it.Due < cutoff {
			dueBy = dueBy.Add(it.Amount)
		}
	}
	if dueBy.GreaterThan(paid) {
		return PlanBroken
	}
	return PlanActive
}

// CheckPlans updates every active plan once a day: what has been paid since it started, and
// whether it is completed or broken. A broken plan releases the ladder and tells finance and the
// manager. Bounded by limit accounts per run.
func (s *Service) CheckPlans(ctx context.Context, tenants []uuid.UUID, now time.Time, limit int) (int, error) {
	today := now.In(s.loc)
	day := today.Format("2006-01-02")
	checked := 0
	for _, tid := range tenants {
		if checked >= limit {
			break
		}
		tctx := tenantguard.With(ctx, tid)
		accs, err := s.client.UnitAccount.Query().Where(func(sel *entsql.Selector) {
			sel.Where(entsql.And(
				sqljson.ValueEQ(sel.C(unitaccount.FieldMetadata), PlanActive, sqljson.Path("ladder", "plan", "status")),
				entsql.Or(entsql.Not(sqljson.HasKey(sel.C(unitaccount.FieldMetadata), sqljson.Path("ladder", "plan", "checked"))),
					entsql.Not(sqljson.ValueEQ(sel.C(unitaccount.FieldMetadata), day, sqljson.Path("ladder", "plan", "checked"))))))
		}).WithUnit().Limit(limit - checked).All(tctx)
		if err != nil {
			return checked, err
		}
		for _, acc := range accs {
			checked++
			if err := s.checkPlan(tctx, tid, acc, today); err != nil {
				s.log.Warn("payment plan check", zap.String("account", acc.AccountRef), zap.Error(err))
			}
		}
	}
	return checked, nil
}

func (s *Service) checkPlan(ctx context.Context, tenantID uuid.UUID, acc *ent.UnitAccount, today time.Time) error {
	l := ReadLadder(acc)
	if !l.Plan.active() {
		return nil
	}
	start, err := time.ParseInLocation("2006-01-02", l.Plan.Start, s.loc)
	if err != nil {
		return err
	}
	var sum []struct {
		Paid decimal.Decimal `json:"paid"`
	}
	if err := s.client.AccountCollection.Query().Where(accountcollection.UnitAccountID(acc.ID), accountcollection.DayGTE(start)).
		Aggregate(func(sel *entsql.Selector) string {
			return entsql.As("COALESCE(SUM("+sel.C(accountcollection.FieldAmount)+"),0)", "paid")
		}).Scan(ctx, &sum); err != nil {
		return err
	}
	paid := decimal.Zero
	if len(sum) > 0 {
		paid = sum[0].Paid
	}
	day := today.Format("2006-01-02")
	l.Plan.Paid, l.Plan.Checked = paid, day
	status := planState(*l.Plan, paid, today)
	if status != PlanActive {
		l.Plan.Status, l.Plan.ClosedOn = status, day
	}
	if err := s.writeLadder(ctx, acc, l); err != nil {
		return err
	}
	if status == PlanBroken && acc.Edges.Unit != nil {
		payload := map[string]any{"account_id": acc.ID, "account_ref": acc.AccountRef, "unit_code": acc.Edges.Unit.Code,
			"name": acc.CustomerName, "total": l.Plan.Total.StringFixed(2), "paid": paid.StringFixed(2),
			"balance": acc.Balance.StringFixed(2), "property_id": acc.Edges.Unit.PropertyID}
		if rs, name, err := register.PropertyResponders(ctx, s.client, acc.Edges.Unit.PropertyID, []maskaniuseroutlet.PropertyRole{
			maskaniuseroutlet.PropertyRoleFinance, maskaniuseroutlet.PropertyRolePropertyManager}, 10); err == nil {
			payload["property"] = name
			if len(rs) > 0 {
				payload["responders"] = rs
			}
		}
		return events.Publish(ctx, s.client.OutboxEvent, tenantID, acc.ID.String()+":plan:"+l.Plan.Start, PaymentPlanBroken, payload)
	}
	return nil
}

func itoa(n int) string { return decimal.NewFromInt(int64(n)).String() }
