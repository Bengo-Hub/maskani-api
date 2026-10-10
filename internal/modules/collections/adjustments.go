package collections

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/adjustment"
	"github.com/bengobox/maskani-api/internal/ent/approvalrequest"
	"github.com/bengobox/maskani-api/internal/ent/maskaniuseroutlet"
	"github.com/bengobox/maskani-api/internal/ent/unitaccount"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/approvals"
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
		SetRequestedBy(by.UserID).
		SetMetadata(map[string]any{"account_ref": acc.AccountRef, "unit_code": acc.Edges.Unit.Code,
			"invoice_number": bill.InvoiceNumber, "requested_by_name": by.Name}).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	pid := acc.Edges.Unit.PropertyID
	_, required, err := s.approvals.Submit(ctx, approvals.Submission{Module: adjustmentModule(kind), ObjectID: adj.ID,
		Reference: bill.InvoiceNumber, Amount: in.Amount, PropertyID: &pid, By: by.UserID, ByName: by.Name,
		Meta: map[string]any{"account_ref": acc.AccountRef, "unit_code": acc.Edges.Unit.Code, "account_id": acc.ID.String(),
			"label": adjustmentLabel(kind) + " on " + bill.InvoiceNumber, "reason": in.Reason}})
	if err != nil {
		_ = s.client.Adjustment.DeleteOneID(adj.ID).Exec(ctx)
		return nil, err
	}
	if !required {
		// No rule and no default: nothing to approve, so it is applied straight away.
		if err := s.client.Adjustment.UpdateOneID(adj.ID).SetStatus(adjustment.StatusApproved).Exec(ctx); err != nil {
			return nil, err
		}
		return s.applyAdjustment(ctx, adj.ID)
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

// adjustmentModule is the approvals module that governs an adjustment kind.
func adjustmentModule(kind adjustment.Kind) string {
	if kind == adjustment.KindCreditNote {
		return "credit_note"
	}
	return "adjustment"
}

func adjustmentLabel(kind adjustment.Kind) string {
	if kind == adjustment.KindCreditNote {
		return "Credit note"
	}
	return "Waiver"
}

// ApproveAdjustment decides the current step of the adjustment's approval on the central engine;
// the last step raises the credit note in treasury. An approved adjustment whose credit note did
// not go through is retried by approving again.
func (s *Service) ApproveAdjustment(ctx context.Context, id uuid.UUID, by approvals.Actor, note string) (*ent.Adjustment, error) {
	adj, err := s.client.Adjustment.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	switch adj.Status {
	case adjustment.StatusRejected, adjustment.StatusApplied:
		return nil, httpx.Conflict("this adjustment has already been closed")
	case adjustment.StatusApproved:
		return s.applyAdjustment(ctx, id)
	}
	req, err := s.approvals.Latest(ctx, id)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, httpx.Conflict("this adjustment has no approval waiting")
	}
	if _, err := s.approvals.Decide(ctx, req.ID, by, approvals.Approve, note); err != nil {
		return nil, err
	}
	return s.client.Adjustment.Get(ctx, id)
}

// AfterDecision moves an adjustment on once its approval request is decided: approved raises the
// credit note, rejected closes it. Called by ApproveAdjustment and by the central approvals inbox.
func (s *Service) AfterDecision(ctx context.Context, req *ent.ApprovalRequest) (*ent.Adjustment, error) {
	switch req.Status {
	case approvalrequest.StatusApproved:
		if _, err := s.client.Adjustment.Update().Where(adjustment.ID(req.ObjectID), adjustment.StatusEQ(adjustment.StatusPendingApproval)).
			SetStatus(adjustment.StatusApproved).Save(ctx); err != nil {
			return nil, err
		}
		return s.applyAdjustment(ctx, req.ObjectID)
	case approvalrequest.StatusRejected:
		adj, err := s.client.Adjustment.Get(ctx, req.ObjectID)
		if err != nil {
			return nil, err
		}
		meta := map[string]any{}
		for k, v := range adj.Metadata {
			meta[k] = v
		}
		for _, a := range approvals.Actions(req) {
			if a.Status == "rejected" {
				meta["rejected_by_name"], meta["rejected_reason"] = a.ActedByName, a.Comment
			}
		}
		if _, err := s.client.Adjustment.Update().Where(adjustment.ID(adj.ID), adjustment.StatusEQ(adjustment.StatusPendingApproval)).
			SetStatus(adjustment.StatusRejected).SetMetadata(meta).Save(ctx); err != nil {
			return nil, err
		}
	}
	return s.client.Adjustment.Get(ctx, req.ObjectID)
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

// RejectAdjustment rejects the current step of the adjustment's approval; the reason is required.
func (s *Service) RejectAdjustment(ctx context.Context, id uuid.UUID, by approvals.Actor, reason string) (*ent.Adjustment, error) {
	req, err := s.approvals.Latest(ctx, id)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, httpx.Conflict("only a credit waiting for approval can be rejected")
	}
	if _, err := s.approvals.Decide(ctx, req.ID, by, approvals.Reject, reason); err != nil {
		return nil, err
	}
	return s.client.Adjustment.Get(ctx, id)
}
