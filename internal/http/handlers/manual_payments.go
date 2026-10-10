package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/collections"
	"github.com/bengobox/maskani-api/internal/shared/page"
)

func displayName(r *http.Request) string {
	a := access(r)
	if a == nil {
		return ""
	}
	if a.LocalUser != nil && a.LocalUser.Name != "" {
		return a.LocalUser.Name
	}
	return a.Email
}

// SubmitManualPayment is POST /unit-accounts/{id}/manual-payments {amount, method, reference,
// paid_on, payer_name, note, evidence_key}: staff record a bank, cash, cheque or typed M-Pesa
// payment; it waits for review.
func (h *H) SubmitManualPayment(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok || !h.accountScope(w, r, id) {
		return
	}
	var in collections.ManualInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	mp, err := h.Collections.SubmitManual(r.Context(), id, collections.Submitter{UserID: actor(r), Name: displayName(r)}, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, mp)
}

// MySubmitManualPayment is POST /me/accounts/{id}/manual-payments: a resident gives the reference
// of a bank transfer or cheque for an amount M-Pesa cannot take; staff verify it before it counts.
func (h *H) MySubmitManualPayment(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	if err := h.Portal.OwnsAccount(r.Context(), access(r).PartyIDs, id); err != nil {
		httpx.Fail(w, err)
		return
	}
	var in collections.ManualInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	in.Evidence = "" // residents attach nothing a reviewer would have to trust blindly
	mp, err := h.Collections.SubmitManual(r.Context(), id, collections.Submitter{UserID: actor(r), Name: displayName(r), Portal: true}, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, mp)
}

// ListManualPayments is GET /collections/manual-payments?status=&property_id=&account_id=
// (keyset): the review queue and its history.
func (h *H) ListManualPayments(w http.ResponseWriter, r *http.Request) {
	ids, all, ok := scopeIDs(w, r)
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	if status != "" && status != "pending" && status != "approved" && status != "rejected" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "status must be pending, approved or rejected")
		return
	}
	accID := httpx.QueryUUID(r, "account_id")
	if accID != nil && !h.accountScope(w, r, *accID) {
		return
	}
	res, err := h.Collections.ListManual(r.Context(), status, ids, all, accID, page.Parse(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	objIDs := make([]uuid.UUID, len(res.Data))
	for i, m := range res.Data {
		objIDs[i] = m.ID
	}
	h.withApprovals(w, r, res, objIDs)
}

func (h *H) manualScope(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return uuid.Nil, false
	}
	pid, err := h.Collections.ManualPropertyID(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return uuid.Nil, false
	}
	return id, requireProperty(w, r, pid)
}

// ApproveManualPayment is POST /manual-payments/{id}/approve {note}: books it in treasury.
func (h *H) ApproveManualPayment(w http.ResponseWriter, r *http.Request) {
	id, ok := h.manualScope(w, r)
	if !ok {
		return
	}
	var in struct {
		Note string `json:"note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in) // the note is optional; an empty body is fine
	mp, err := h.Collections.ApproveManual(r.Context(), id, approver(r), in.Note)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	h.Reports.Invalidate(access(r).TenantID)
	httpx.JSON(w, http.StatusOK, mp)
}

// RejectManualPayment is POST /manual-payments/{id}/reject {reason}.
func (h *H) RejectManualPayment(w http.ResponseWriter, r *http.Request) {
	id, ok := h.manualScope(w, r)
	if !ok {
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	mp, err := h.Collections.RejectManual(r.Context(), id, approver(r), in.Reason)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, mp)
}

// ImportBankLines is POST /collections/bank-lines {fund, property_id, lines: [{date, amount,
// reference, description, payer}]}: bank statement credits matched to accounts (unit reference in
// the text, else the owner's phone) and queued for review as bank transfers. Returns each line's
// outcome: queued, duplicate, unmatched or invalid.
func (h *H) ImportBankLines(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Fund       string                 `json:"fund"`
		PropertyID *uuid.UUID             `json:"property_id"`
		Lines      []collections.BankLine `json:"lines"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	a := access(r)
	ids, all := a.PropertyIDs, a.AllProperties || a.Bypass
	if in.PropertyID != nil {
		if !requireProperty(w, r, *in.PropertyID) {
			return
		}
		ids, all = []uuid.UUID{*in.PropertyID}, false
	}
	if in.Fund == "" {
		in.Fund = "estate"
	}
	res, err := h.Collections.ImportBankLines(r.Context(), in.Fund, ids, all,
		collections.Submitter{UserID: actor(r), Name: displayName(r)}, in.Lines)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": res})
}
