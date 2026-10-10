package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/collections"
	"github.com/bengobox/maskani-api/internal/shared/page"
)

// scopeIDs resolves the property scope of a queue list: one property when asked for, else the
// caller's properties (all for unrestricted staff).
func scopeIDs(w http.ResponseWriter, r *http.Request) ([]uuid.UUID, bool, bool) {
	f, ok := scopeFilter(w, r)
	if !ok {
		return nil, false, false
	}
	ids, all := f.Scope, f.AllProperties || access(r).Bypass
	if f.PropertyID != nil {
		ids, all = []uuid.UUID{*f.PropertyID}, false
	}
	return ids, all, true
}

// RequestAdjustment is POST /unit-accounts/{id}/adjustments {kind, invoice_id, amount, reason}: a
// credit note or waiver on one unpaid bill, waiting for approval.
func (h *H) RequestAdjustment(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok || !h.accountScope(w, r, id) {
		return
	}
	var in collections.AdjustmentInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	adj, err := h.Collections.RequestAdjustment(r.Context(), id, collections.Submitter{UserID: actor(r), Name: displayName(r)}, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, adj)
}

// ListAdjustments is GET /collections/adjustments?status=&property_id=&account_id= (keyset).
func (h *H) ListAdjustments(w http.ResponseWriter, r *http.Request) {
	ids, all, ok := scopeIDs(w, r)
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	switch status {
	case "", "pending_approval", "approved", "rejected", "applied":
	default:
		httpx.Error(w, http.StatusBadRequest, "bad_request", "status must be pending_approval, approved, rejected or applied")
		return
	}
	accID := httpx.QueryUUID(r, "account_id")
	if accID != nil && !h.accountScope(w, r, *accID) {
		return
	}
	res, err := h.Collections.ListAdjustments(r.Context(), status, ids, all, accID, page.Parse(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	objIDs := make([]uuid.UUID, len(res.Data))
	for i, a := range res.Data {
		objIDs[i] = a.ID
	}
	h.withApprovals(w, r, res, objIDs)
}

func (h *H) adjustmentScope(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return uuid.Nil, false
	}
	pid, err := h.Collections.AdjustmentPropertyID(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return uuid.Nil, false
	}
	return id, requireProperty(w, r, pid)
}

// ApproveAdjustment is POST /adjustments/{id}/approve {note}: adds an approval; the last one the
// rule needs raises the credit note in treasury.
func (h *H) ApproveAdjustment(w http.ResponseWriter, r *http.Request) {
	id, ok := h.adjustmentScope(w, r)
	if !ok {
		return
	}
	var in struct {
		Note string `json:"note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in) // the note is optional
	adj, err := h.Collections.ApproveAdjustment(r.Context(), id, approver(r), in.Note)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	h.Reports.Invalidate(access(r).TenantID)
	httpx.JSON(w, http.StatusOK, adj)
}

// RejectAdjustment is POST /adjustments/{id}/reject {reason}.
func (h *H) RejectAdjustment(w http.ResponseWriter, r *http.Request) {
	id, ok := h.adjustmentScope(w, r)
	if !ok {
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	adj, err := h.Collections.RejectAdjustment(r.Context(), id, approver(r), in.Reason)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, adj)
}

// ListBillQueries is GET /collections/bill-queries?status=&property_id= (keyset): the finance queue.
func (h *H) ListBillQueries(w http.ResponseWriter, r *http.Request) {
	ids, all, ok := scopeIDs(w, r)
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	switch status {
	case "", "open", "in_review", "resolved", "rejected":
	default:
		httpx.Error(w, http.StatusBadRequest, "bad_request", "status must be open, in_review, resolved or rejected")
		return
	}
	res, err := h.Collections.ListBillQueries(r.Context(), status, ids, all, nil, page.Parse(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

// AnswerBillQuery is POST /bill-queries/{id}/answer {status, resolution}: take it (in_review), or
// resolve or reject it with the answer the resident sees.
func (h *H) AnswerBillQuery(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	pid, err := h.Collections.BillQueryPropertyID(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if !requireProperty(w, r, pid) {
		return
	}
	var in collections.BillQueryAnswer
	if !httpx.Decode(w, r, &in) {
		return
	}
	q, err := h.Collections.AnswerBillQuery(r.Context(), id, collections.Reviewer{UserID: actor(r), Name: displayName(r)}, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, q)
}

// MyRaiseBillQuery is POST /me/accounts/{id}/bill-queries {invoice_id, subject, body}.
func (h *H) MyRaiseBillQuery(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	a := access(r)
	if err := h.Portal.OwnsAccount(r.Context(), a.PartyIDs, id); err != nil {
		httpx.Fail(w, err)
		return
	}
	var in collections.BillQueryInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	by := collections.Raiser{UserID: actor(r), Name: displayName(r)}
	if len(a.PartyIDs) > 0 {
		by.PartyID = &a.PartyIDs[0]
	}
	q, err := h.Collections.RaiseBillQuery(r.Context(), id, by, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, q)
}

// MyBillQueries is GET /me/bill-queries (keyset): the caller's queries across their accounts.
func (h *H) MyBillQueries(w http.ResponseWriter, r *http.Request) {
	units, err := h.Portal.Units(r.Context(), access(r).PartyIDs)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	accIDs := []uuid.UUID{}
	for _, u := range units {
		for _, a := range u.Accounts {
			accIDs = append(accIDs, a.ID)
		}
	}
	res, err := h.Collections.ListBillQueries(r.Context(), "", nil, true, accIDs, page.Parse(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}
