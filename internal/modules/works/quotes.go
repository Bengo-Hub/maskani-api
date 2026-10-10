package works

import (
	"context"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/approvalrequest"
	"github.com/bengobox/maskani-api/internal/ent/workorder"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/approvals"
	"github.com/bengobox/maskani-api/internal/modules/rbac"
)

// quoteModule is the approvals module for work order quotes.
const quoteModule = "work_order_quote"

// SetApprovals puts quote approval on the central engine: by default one approval by someone who
// manages works (never the person who entered the quote); an estate's rules set more steps for
// larger quotes.
func (s *Service) SetApprovals(ap *approvals.Service) {
	s.approvals = ap
	ap.SetDefault(quoteModule, approvals.Step{Sequence: 1, Name: "Approve the quote", Permission: rbac.PermWorksManage})
	ap.OnDecision(quoteModule, func(ctx context.Context, req *ent.ApprovalRequest) error {
		var last approvals.Action
		for _, a := range approvals.Actions(req) {
			if a.Status == "approved" || a.Status == "rejected" {
				last = a
			}
		}
		by := uuid.Nil
		if last.ActedBy != nil {
			by = *last.ActedBy
		}
		return s.applyQuote(ctx, req.ObjectID, req.Status == approvalrequest.StatusApproved, by, last.Comment)
	})
}

// submitQuote sends a newly entered quote for approval; with nothing to approve it is approved at once.
func (s *Service) submitQuote(ctx context.Context, wo *ent.WorkOrder, by uuid.UUID) error {
	if s.approvals == nil {
		return nil
	}
	pid := wo.PropertyID
	amount := decimal.Zero
	if wo.QuoteAmount != nil {
		amount = *wo.QuoteAmount
	}
	_, required, err := s.approvals.Submit(ctx, approvals.Submission{Module: quoteModule, ObjectID: wo.ID, Reference: wo.Number,
		Amount: amount, PropertyID: &pid, By: by,
		Meta: map[string]any{"label": "Quote for " + wo.Number + ", " + wo.Title, "work_order_id": wo.ID.String()}})
	if err != nil || required {
		return err
	}
	return s.applyQuote(ctx, wo.ID, true, by, "No approval needed for this amount")
}

// DecideQuote approves or rejects the current step of a quote's approval.
func (s *Service) DecideQuote(ctx context.Context, id uuid.UUID, by approvals.Actor, d approvals.Decision, note string) (*ent.WorkOrder, error) {
	if s.approvals == nil {
		return nil, httpx.Unavailable("approvals are not set up")
	}
	req, err := s.approvals.Latest(ctx, id)
	if err != nil {
		return nil, err
	}
	if req == nil || req.Status != approvalrequest.StatusPending {
		return nil, httpx.Conflict("this work order has no quote waiting for approval")
	}
	if _, err := s.approvals.Decide(ctx, req.ID, by, d, note); err != nil {
		return nil, err
	}
	return s.client.WorkOrder.Get(ctx, id)
}

// applyQuote moves a quoted work order on once its approval is decided: approved lets the work
// start; rejected sends it back to the assignee for a new quote. Recorded on the timeline.
func (s *Service) applyQuote(ctx context.Context, id uuid.UUID, approved bool, by uuid.UUID, note string) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	to, qs, kind := workorder.StatusApproved, workorder.QuoteStatusApproved, "approve_quote"
	if !approved {
		to, qs, kind = workorder.StatusAssigned, workorder.QuoteStatusRejected, "reject_quote"
	}
	n, err := tx.WorkOrder.Update().Where(workorder.ID(id), workorder.StatusEQ(workorder.StatusQuoted)).
		SetStatus(to).SetQuoteStatus(qs).Save(ctx)
	if err != nil || n == 0 {
		_ = tx.Rollback()
		return err // already moved on: nothing to do
	}
	if err := s.event(ctx, tx, id, Actor{UserID: by, Kind: "staff"}, kind, string(workorder.StatusQuoted), string(to), note); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if wo, err := s.client.WorkOrder.Get(ctx, id); err == nil {
		s.emit(ctx, wo)
	}
	return nil
}
