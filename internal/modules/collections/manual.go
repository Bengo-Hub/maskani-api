package collections

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/approvalrequest"
	"github.com/bengobox/maskani-api/internal/ent/manualpayment"
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

// MpesaPromptLimit is the most one M-Pesa prompt can take. Above it the pay screens offer bank and
// cheque instead, recorded here for review.
var MpesaPromptLimit = decimal.NewFromInt(250000)

// Manual payment events: reviewers are told when one is waiting.
const ManualPaymentSubmitted = "manual_payment.submitted"

// ManualInput records a payment that did not come through a gateway.
type ManualInput struct {
	Amount    decimal.Decimal `json:"amount"`
	Method    string          `json:"method"` // bank_transfer, cash, cheque, mpesa
	Reference string          `json:"reference"`
	PaidOn    string          `json:"paid_on"` // YYYY-MM-DD, default today
	PayerName string          `json:"payer_name"`
	Note      string          `json:"note"`
	Evidence  string          `json:"evidence_key"`
}

// Submitter is who records the payment: staff, or a resident from the portal (who may only offer
// bank and cheque, for amounts M-Pesa cannot take).
type Submitter struct {
	UserID uuid.UUID
	Name   string
	Portal bool
	// Quiet skips the reviewer alert per entry (a statement import; its lines show in the queue).
	Quiet bool
}

var staffMethods = map[string]bool{"bank_transfer": true, "cash": true, "cheque": true, "mpesa": true}
var portalMethods = map[string]bool{"bank_transfer": true, "cheque": true}

// SubmitManual records a manual payment as pending review and tells the property's reviewers.
func (s *Service) SubmitManual(ctx context.Context, accountID uuid.UUID, by Submitter, in ManualInput) (*ent.ManualPayment, error) {
	in.Reference = strings.ToUpper(strings.TrimSpace(in.Reference))
	allowed := staffMethods
	if by.Portal {
		allowed = portalMethods
	}
	switch {
	case !allowed[in.Method]:
		if by.Portal {
			return nil, httpx.Invalid("pay by bank transfer or cheque here; M-Pesa payments go through Pay now")
		}
		return nil, httpx.Invalid("method must be bank_transfer, cash, cheque or mpesa")
	case !in.Amount.IsPositive():
		return nil, httpx.Invalid("enter an amount above zero")
	case in.Reference == "" || len(in.Reference) > 60:
		return nil, httpx.Invalid("enter the payment reference (bank slip, cheque number or M-Pesa code)")
	}
	paidOn := time.Now()
	if in.PaidOn != "" {
		t, err := time.Parse("2006-01-02", in.PaidOn)
		if err != nil || t.After(time.Now()) {
			return nil, httpx.Invalid("the payment date must be today or earlier")
		}
		paidOn = t
	}
	acc, err := s.client.UnitAccount.Query().Where(unitaccount.ID(accountID)).WithUnit().Only(ctx)
	if err != nil {
		return nil, err
	}
	if acc.Edges.Unit == nil {
		return nil, httpx.Invalid("this account has no unit")
	}
	note := strings.TrimSpace(in.Note)
	if len(note) > 500 {
		note = note[:500]
	}
	mp, err := s.client.ManualPayment.Create().SetUnitAccountID(acc.ID).SetPropertyID(acc.Edges.Unit.PropertyID).
		SetAmount(in.Amount).SetMethod(manualpayment.Method(in.Method)).SetReference(in.Reference).SetPaidOn(paidOn).
		SetPayerName(strings.TrimSpace(in.PayerName)).SetNote(note).SetEvidenceKey(in.Evidence).
		SetSubmittedBy(by.UserID).SetSubmittedByName(by.Name).
		SetMetadata(map[string]any{"account_ref": acc.AccountRef, "unit_code": acc.Edges.Unit.Code, "portal": by.Portal}).
		Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return nil, httpx.Conflict("this reference has already been recorded for review")
		}
		return nil, err
	}
	pid := acc.Edges.Unit.PropertyID
	if _, _, err := s.approvals.Submit(ctx, approvals.Submission{Module: "manual_payment", ObjectID: mp.ID, Reference: in.Reference,
		Amount: in.Amount, PropertyID: &pid, By: by.UserID, ByName: by.Name,
		Meta: map[string]any{"account_ref": acc.AccountRef, "unit_code": acc.Edges.Unit.Code, "account_id": acc.ID.String(),
			"label": manualLabel(in.Method) + " " + in.Reference, "portal": by.Portal}}); err != nil {
		_ = s.client.ManualPayment.DeleteOneID(mp.ID).Exec(ctx)
		return nil, err
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	payload := map[string]any{"manual_payment_id": mp.ID, "account_id": acc.ID, "account_ref": acc.AccountRef,
		"unit_code": acc.Edges.Unit.Code, "amount": in.Amount.StringFixed(2), "method": in.Method, "reference": in.Reference,
		"submitted_by": by.Name, "property_id": acc.Edges.Unit.PropertyID}
	if rs, name, err := register.PropertyResponders(ctx, s.client, acc.Edges.Unit.PropertyID, []maskaniuseroutlet.PropertyRole{
		maskaniuseroutlet.PropertyRolePropertyManager, maskaniuseroutlet.PropertyRoleCaretaker}, 10); err == nil {
		payload["property"] = name
		if len(rs) > 0 {
			payload["responders"] = rs
		}
	}
	if !by.Quiet {
		_ = events.Publish(ctx, s.client.OutboxEvent, tenantID, mp.ID.String(), ManualPaymentSubmitted, payload)
	}
	return mp, nil
}

// ListManual returns a keyset page of manual payments (newest first) by status in the given
// properties (all when all is true).
func (s *Service) ListManual(ctx context.Context, status string, propertyIDs []uuid.UUID, all bool, accountID *uuid.UUID, p page.Params) (page.Result[*ent.ManualPayment], error) {
	q := s.client.ManualPayment.Query()
	if status != "" {
		q = q.Where(manualpayment.StatusEQ(manualpayment.Status(status)))
	}
	if accountID != nil {
		q = q.Where(manualpayment.UnitAccountID(*accountID))
	}
	if !all {
		q = q.Where(manualpayment.PropertyIDIn(propertyIDs...))
	}
	rows, err := q.Where(p.Predicate()).Modify(page.Order()).Limit(p.Limit + 1).All(ctx)
	if err != nil {
		return page.Result[*ent.ManualPayment]{}, err
	}
	return page.Build(rows, p.Limit, func(m *ent.ManualPayment) (uuid.UUID, time.Time) { return m.ID, m.CreatedAt }), nil
}

// ManualPropertyID returns the property of a manual payment (for scope checks).
func (s *Service) ManualPropertyID(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	mp, err := s.client.ManualPayment.Get(ctx, id)
	if err != nil {
		return uuid.Nil, err
	}
	return mp.PropertyID, nil
}

// Reviewer is who answers a bill query.
type Reviewer struct {
	UserID uuid.UUID
	Name   string
}

func manualLabel(method string) string {
	switch method {
	case "bank_transfer":
		return "Bank transfer"
	case "cheque":
		return "Cheque"
	case "cash":
		return "Cash"
	}
	return "M-Pesa"
}

// ApproveManual decides the current step of the payment's verification on the central engine. The
// last step books it in treasury and closes it; a failure to book leaves it pending with the
// approval done, so approving again retries the booking. The recorder never verifies their own.
func (s *Service) ApproveManual(ctx context.Context, id uuid.UUID, by approvals.Actor, note string) (*ent.ManualPayment, error) {
	mp, err := s.client.ManualPayment.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if mp.Status != manualpayment.StatusPending {
		return nil, httpx.Conflict("this payment has already been reviewed")
	}
	req, err := s.approvals.Latest(ctx, id)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, httpx.Conflict("this payment has no verification waiting")
	}
	if req.Status == approvalrequest.StatusPending {
		if _, err := s.approvals.Decide(ctx, req.ID, by, approvals.Approve, note); err != nil {
			return nil, err
		}
	} else if err := s.approvals.Retry(ctx, req); err != nil {
		return nil, err
	}
	return s.client.ManualPayment.Get(ctx, id)
}

// AfterManualDecision moves a manual payment on once its verification is decided: approved books it
// in treasury, rejected closes it with the reason. Called by ApproveManual, RejectManual and the
// central approvals inbox.
func (s *Service) AfterManualDecision(ctx context.Context, req *ent.ApprovalRequest) (*ent.ManualPayment, error) {
	mp, err := s.client.ManualPayment.Get(ctx, req.ObjectID)
	if err != nil {
		return nil, err
	}
	if mp.Status != manualpayment.StatusPending {
		return mp, nil
	}
	acts := approvals.Actions(req)
	var last approvals.Action
	for _, a := range acts {
		if a.Status == "approved" || a.Status == "rejected" {
			last = a
		}
	}
	reviewer, name := uuid.Nil, last.ActedByName
	if last.ActedBy != nil {
		reviewer = *last.ActedBy
	}
	switch req.Status {
	case approvalrequest.StatusRejected:
		_, err := s.client.ManualPayment.Update().Where(manualpayment.ID(mp.ID), manualpayment.StatusEQ(manualpayment.StatusPending)).
			SetStatus(manualpayment.StatusRejected).SetReviewedBy(reviewer).SetReviewedByName(name).SetReviewedAt(time.Now()).
			SetReviewNote(last.Comment).Save(ctx)
		if err != nil {
			return nil, err
		}
		return s.client.ManualPayment.Get(ctx, mp.ID)
	case approvalrequest.StatusApproved:
	default:
		return mp, nil // more steps to go
	}
	acc, err := s.client.UnitAccount.Query().Where(unitaccount.ID(mp.UnitAccountID)).WithFund().Only(ctx)
	if err != nil {
		return nil, err
	}
	fund := ""
	if acc.Edges.Fund != nil {
		fund = acc.Edges.Fund.Code
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	intentID, err := s.treasury.RecordManualPayment(ctx, tenantID, acc.AccountRef, treasury.ManualPaymentRequest{
		AccountID: acc.ID.String(), Fund: fund, Amount: mp.Amount, Method: string(mp.Method), Reference: mp.Reference,
		PayerName: mp.PayerName,
	})
	if err != nil {
		return nil, httpx.Unavailable("verified, but the payment could not be booked in the accounts; verify again shortly to retry")
	}
	// Only the first booking closes it, even if two retries land at the same moment.
	if _, err := s.client.ManualPayment.Update().Where(manualpayment.ID(mp.ID), manualpayment.StatusEQ(manualpayment.StatusPending)).
		SetStatus(manualpayment.StatusApproved).SetReviewedBy(reviewer).SetReviewedByName(name).SetReviewedAt(time.Now()).
		SetReviewNote(last.Comment).SetTreasuryIntentID(intentID).Save(ctx); err != nil {
		return nil, err
	}
	if _, err := s.accounts.Refresh(ctx, acc); err != nil {
		s.log.Warn("balance refresh after manual payment")
	}
	return s.client.ManualPayment.Get(ctx, mp.ID)
}

// RejectManual rejects the payment's verification; the reason is required.
func (s *Service) RejectManual(ctx context.Context, id uuid.UUID, by approvals.Actor, reason string) (*ent.ManualPayment, error) {
	req, err := s.approvals.Latest(ctx, id)
	if err != nil {
		return nil, err
	}
	if req == nil || req.Status != approvalrequest.StatusPending {
		return nil, httpx.Conflict("this payment has already been reviewed")
	}
	if _, err := s.approvals.Decide(ctx, req.ID, by, approvals.Reject, reason); err != nil {
		return nil, err
	}
	return s.client.ManualPayment.Get(ctx, id)
}
