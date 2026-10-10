package collections

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/adjustment"
	"github.com/bengobox/maskani-api/internal/ent/approvalrule"
	"github.com/bengobox/maskani-api/internal/ent/maskaniuseroutlet"
	"github.com/bengobox/maskani-api/internal/ent/unitaccount"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/register"
	"github.com/bengobox/maskani-api/internal/modules/treasury"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
	"github.com/bengobox/maskani-api/internal/shared/page"
)

// Adjustment events: approvers hear of a request; the account holder's statement shows the credit.
const (
	AdjustmentRequested = "adjustment.requested"
	AdjustmentApplied   = "adjustment.applied"
)

// AdjustmentInput asks for part of an unpaid bill to be credited (a credit note for a billing
// mistake, or a waiver of a charge the estate agrees to forgive).
type AdjustmentInput struct {
	Kind      string          `json:"kind"` // credit_note or waiver
	InvoiceID uuid.UUID       `json:"invoice_id"`
	Amount    decimal.Decimal `json:"amount"`
	Reason    string          `json:"reason"`
}

// Approver is who approves an adjustment, with their role codes for rules that name roles.
type Approver struct {
	UserID uuid.UUID
	Name   string
	Roles  []string
}

// ruleAction maps an adjustment kind to the approval rule action that governs it.
func ruleAction(kind adjustment.Kind) approvalrule.Action {
	if kind == adjustment.KindCreditNote {
		return approvalrule.ActionCreditNote
	}
	return approvalrule.ActionAdjustment
}

// rule finds the active rule whose amount band holds the amount. No rule means one approval by
// anyone who may approve (billing.approve), never the requester.
func (s *Service) rule(ctx context.Context, kind adjustment.Kind, amount decimal.Decimal) (*ent.ApprovalRule, error) {
	rules, err := s.client.ApprovalRule.Query().Where(approvalrule.ActionEQ(ruleAction(kind)), approvalrule.Active(true)).
		Order(ent.Desc(approvalrule.FieldMinAmount)).Limit(20).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range rules {
		if amount.GreaterThanOrEqual(r.MinAmount) && (r.MaxAmount == nil || amount.LessThanOrEqual(*r.MaxAmount)) {
			return r, nil
		}
	}
	return nil, nil
}

// RequestAdjustment records a credit for review. The bill must belong to the account and still owe
// at least the amount (treasury never credits money already paid).
func (s *Service) RequestAdjustment(ctx context.Context, accountID uuid.UUID, by Submitter, in AdjustmentInput) (*ent.Adjustment, error) {
	kind := adjustment.Kind(in.Kind)
	in.Reason = strings.TrimSpace(in.Reason)
	switch {
	case kind != adjustment.KindCreditNote && kind != adjustment.KindWaiver:
		return nil, httpx.Invalid("kind must be credit_note or waiver")
	case !in.Amount.IsPositive():
		return nil, httpx.Invalid("enter an amount above zero")
	case in.Reason == "" || len(in.Reason) > 500:
		return nil, httpx.Invalid("say why the bill is being credited (up to 500 characters)")
	case in.InvoiceID == uuid.Nil:
		return nil, httpx.Invalid("choose the bill to credit")
	}
	acc, err := s.client.UnitAccount.Query().Where(unitaccount.ID(accountID)).WithUnit().Only(ctx)
	if err != nil {
		return nil, err
	}
	if acc.Edges.Unit == nil {
		return nil, httpx.Invalid("this account has no unit")
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	led, err := s.treasury.Ledger(ctx, tenantID, acc.AccountRef, 200)
	if err != nil {
		return nil, httpx.Unavailable("the accounts service is not answering; try again shortly")
	}
	var bill *treasury.LedgerInvoice
	for i := range led.Invoices {
		if led.Invoices[i].ID == in.InvoiceID {
			bill = &led.Invoices[i]
		}
	}
	if bill == nil {
		return nil, httpx.Invalid("that bill is not on this account")
	}
	if owed := bill.TotalAmount.Sub(bill.AmountPaid).Sub(bill.AmountCredited); in.Amount.GreaterThan(owed) {
		return nil, httpx.Invalid("the bill only has " + owed.StringFixed(2) + " unpaid; a credit cannot reverse money already paid")
	}
	// One open request per bill, so two people cannot credit the same money twice.
	if n, err := s.client.Adjustment.Query().Where(adjustment.TreasuryInvoiceID(in.InvoiceID),
		adjustment.StatusIn(adjustment.StatusPendingApproval, adjustment.StatusApproved)).Count(ctx); err != nil {
		return nil, err
	} else if n > 0 {
		return nil, httpx.Conflict("this bill already has a credit waiting for approval")
	}
	adj, err := s.client.Adjustment.Create().SetUnitAccountID(acc.ID).SetPropertyID(acc.Edges.Unit.PropertyID).
		SetKind(kind).SetAmount(in.Amount).SetReason(in.Reason).SetTreasuryInvoiceID(in.InvoiceID).
		SetRequestedBy(by.UserID).SetApprovals([]map[string]any{}).
		SetMetadata(map[string]any{"account_ref": acc.AccountRef, "unit_code": acc.Edges.Unit.Code,
			"invoice_number": bill.InvoiceNumber, "requested_by_name": by.Name}).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{"adjustment_id": adj.ID, "account_id": acc.ID, "account_ref": acc.AccountRef,
		"unit_code": acc.Edges.Unit.Code, "kind": string(kind), "amount": in.Amount.StringFixed(2), "reason": in.Reason,
		"invoice_number": bill.InvoiceNumber, "requested_by": by.Name, "property_id": acc.Edges.Unit.PropertyID}
	if rs, name, err := register.PropertyResponders(ctx, s.client, acc.Edges.Unit.PropertyID, []maskaniuseroutlet.PropertyRole{
		maskaniuseroutlet.PropertyRoleFinance, maskaniuseroutlet.PropertyRolePropertyManager}, 10); err == nil {
		payload["property"] = name
		if len(rs) > 0 {
			payload["responders"] = rs
		}
	}
	_ = events.Publish(ctx, s.client.OutboxEvent, tenantID, adj.ID.String(), AdjustmentRequested, payload)
	return adj, nil
}

// ListAdjustments returns a keyset page (newest first) by status in the given properties.
func (s *Service) ListAdjustments(ctx context.Context, status string, propertyIDs []uuid.UUID, all bool, accountID *uuid.UUID, p page.Params) (page.Result[*ent.Adjustment], error) {
	q := s.client.Adjustment.Query()
	if status != "" {
		q = q.Where(adjustment.StatusEQ(adjustment.Status(status)))
	}
	if accountID != nil {
		q = q.Where(adjustment.UnitAccountID(*accountID))
	}
	if !all {
		q = q.Where(adjustment.PropertyIDIn(propertyIDs...))
	}
	rows, err := q.Where(p.Predicate()).Modify(page.Order()).Limit(p.Limit + 1).All(ctx)
	if err != nil {
		return page.Result[*ent.Adjustment]{}, err
	}
	return page.Build(rows, p.Limit, func(a *ent.Adjustment) (uuid.UUID, time.Time) { return a.ID, a.CreatedAt }), nil
}

// AdjustmentPropertyID returns the property of an adjustment (for scope checks).
func (s *Service) AdjustmentPropertyID(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	a, err := s.client.Adjustment.Query().Where(adjustment.ID(id)).Select(adjustment.FieldPropertyID).Only(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	return a.PropertyID, nil
}

// ApproveAdjustment adds the caller's approval. When the rule's levels are reached the credit note
// is raised in treasury and the adjustment is applied. The requester never approves their own, one
// person approves once, and a rule naming roles admits only those roles. A treasury failure leaves
// it approved so approving again retries the credit note.
func (s *Service) ApproveAdjustment(ctx context.Context, id uuid.UUID, by Approver, note string) (*ent.Adjustment, error) {
	adj, err := s.client.Adjustment.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if adj.Status == adjustment.StatusRejected || adj.Status == adjustment.StatusApplied {
		return nil, httpx.Conflict("this adjustment has already been closed")
	}
	if adj.RequestedBy == by.UserID {
		return nil, httpx.Forbidden("someone other than the person who asked for it must approve this credit")
	}
	levels := 1
	r, err := s.rule(ctx, adj.Kind, adj.Amount)
	if err != nil {
		return nil, err
	}
	if r != nil {
		levels = max(r.Levels, 1)
		if len(r.ApproverRoles) > 0 && !slices.ContainsFunc(by.Roles, func(role string) bool { return slices.Contains(r.ApproverRoles, role) }) {
			return nil, httpx.Forbidden("this amount needs approval by " + strings.Join(r.ApproverRoles, " or "))
		}
	}
	approvals := adj.Approvals
	if adj.Status == adjustment.StatusPendingApproval {
		for _, a := range approvals {
			if a["user_id"] == by.UserID.String() {
				return nil, httpx.Conflict("you have already approved this; it needs another approver")
			}
		}
		approvals = append(approvals, map[string]any{"user_id": by.UserID.String(), "name": by.Name,
			"at": time.Now().UTC().Format(time.RFC3339), "note": strings.TrimSpace(note)})
		next := adjustment.StatusPendingApproval
		if len(approvals) >= levels {
			next = adjustment.StatusApproved
		}
		// Conditional on the approvals seen, so two approvers at once cannot both count as the last.
		n, err := s.client.Adjustment.Update().Where(adjustment.ID(id), adjustment.StatusEQ(adjustment.StatusPendingApproval),
			adjustment.UpdatedAt(adj.UpdatedAt)).SetApprovals(approvals).SetStatus(next).Save(ctx)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, httpx.Conflict("someone else approved this at the same moment; reload and try again")
		}
		if next != adjustment.StatusApproved {
			return s.client.Adjustment.Get(ctx, id)
		}
	}
	return s.applyAdjustment(ctx, id)
}

// applyAdjustment raises the credit note for an approved adjustment, once.
func (s *Service) applyAdjustment(ctx context.Context, id uuid.UUID) (*ent.Adjustment, error) {
	adj, err := s.client.Adjustment.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if adj.Status != adjustment.StatusApproved || adj.TreasuryInvoiceID == nil {
		return adj, nil
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	label := "Credit note"
	if adj.Kind == adjustment.KindWaiver {
		label = "Waiver"
	}
	cnID, cnNumber, err := s.treasury.CreateCreditNote(ctx, tenantID, *adj.TreasuryInvoiceID, []treasury.CreditNoteLine{{
		Description: label + ": " + adj.Reason, Quantity: decimal.NewFromInt(1), UnitPrice: adj.Amount,
	}}, "adjustment-"+adj.ID.String())
	if err != nil {
		var he *treasury.HTTPError
		if errors.As(err, &he) && he.Status == 400 {
			return nil, httpx.Invalid("the accounts refused the credit: " + strings.TrimSpace(he.Body))
		}
		return nil, httpx.Unavailable("the credit note could not be raised; approve again shortly to retry")
	}
	meta := map[string]any{}
	for k, v := range adj.Metadata {
		meta[k] = v
	}
	meta["credit_note_number"] = cnNumber
	n, err := s.client.Adjustment.Update().Where(adjustment.ID(id), adjustment.StatusEQ(adjustment.StatusApproved)).
		SetStatus(adjustment.StatusApplied).SetTreasuryCreditNoteID(cnID).SetMetadata(meta).Save(ctx)
	if err != nil {
		return nil, err
	}
	if n > 0 {
		acc, err := s.client.UnitAccount.Get(ctx, adj.UnitAccountID)
		if err == nil {
			if _, err := s.accounts.Refresh(ctx, acc); err != nil {
				s.log.Warn("balance refresh after credit note")
			}
			_ = events.Publish(ctx, s.client.OutboxEvent, tenantID, adj.ID.String(), AdjustmentApplied, map[string]any{
				"adjustment_id": adj.ID, "account_id": acc.ID, "account_ref": acc.AccountRef, "kind": string(adj.Kind),
				"amount": adj.Amount.StringFixed(2), "credit_note_number": cnNumber, "property_id": adj.PropertyID})
		}
	}
	return s.client.Adjustment.Get(ctx, id)
}

// RejectAdjustment closes a request without crediting; the reason is required.
func (s *Service) RejectAdjustment(ctx context.Context, id uuid.UUID, by Approver, reason string) (*ent.Adjustment, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, httpx.Invalid("say why the credit is rejected")
	}
	adj, err := s.client.Adjustment.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	meta := map[string]any{}
	for k, v := range adj.Metadata {
		meta[k] = v
	}
	meta["rejected_by_name"], meta["rejected_reason"], meta["rejected_at"] = by.Name, reason, time.Now().UTC().Format(time.RFC3339)
	n, err := s.client.Adjustment.Update().Where(adjustment.ID(id), adjustment.StatusEQ(adjustment.StatusPendingApproval)).
		SetStatus(adjustment.StatusRejected).SetMetadata(meta).Save(ctx)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, httpx.Conflict("only a credit waiting for approval can be rejected")
	}
	return s.client.Adjustment.Get(ctx, id)
}
