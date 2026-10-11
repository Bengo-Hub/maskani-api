package handlers

import (
	"context"
	"net/http"
	"slices"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/privacy"
	"github.com/bengobox/maskani-api/internal/shared/page"
)

// privacyRequest loads a request for the office and checks the caller may see its person.
func (h *H) privacyRequest(w http.ResponseWriter, r *http.Request) (*ent.PrivacyRequest, bool) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return nil, false
	}
	req, err := h.Privacy.Get(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return nil, false
	}
	if req.PartyID != nil && !h.requireParty(w, r, *req.PartyID) {
		return nil, false
	}
	return req, true
}

// ListPrivacyRequests is GET /privacy/requests (keyset; status, overdue, party_id).
func (h *H) ListPrivacyRequests(w http.ResponseWriter, r *http.Request) {
	f := privacy.Filter{Status: r.URL.Query().Get("status"), Overdue: r.URL.Query().Get("overdue") == "true",
		PartyID: httpx.QueryUUID(r, "party_id")}
	if f.PartyID != nil && !h.requireParty(w, r, *f.PartyID) {
		return
	}
	res, err := h.Privacy.List(r.Context(), f, page.Parse(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

// CreatePrivacyRequest is POST /privacy/requests {party_id, kind, notes}: a request made to the
// office by phone, letter or in person. It waits for the code or an in-person check.
func (h *H) CreatePrivacyRequest(w http.ResponseWriter, r *http.Request) {
	var in privacy.Input
	if !httpx.Decode(w, r, &in) || !h.requireParty(w, r, in.PartyID) {
		return
	}
	req, err := h.Privacy.Create(r.Context(), in, "office", actor(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, req)
}

// SendPrivacyCode is POST /privacy/requests/{id}/send-code: a code to the phone and email on record.
func (h *H) SendPrivacyCode(w http.ResponseWriter, r *http.Request) {
	req, ok := h.privacyRequest(w, r)
	if !ok {
		return
	}
	if err := h.Privacy.SendCode(r.Context(), req.ID); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

// VerifyPrivacyRequest is POST /privacy/requests/{id}/verify {code} or {in_person: true, note}.
func (h *H) VerifyPrivacyRequest(w http.ResponseWriter, r *http.Request) {
	req, ok := h.privacyRequest(w, r)
	if !ok {
		return
	}
	var in struct {
		Code     string `json:"code"`
		InPerson bool   `json:"in_person"`
		Note     string `json:"note"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := h.Privacy.Verify(r.Context(), req.ID, actor(r), in.Code, in.InPerson, in.Note)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// CompletePrivacyRequest is POST /privacy/requests/{id}/complete {text}: what was given or corrected.
func (h *H) CompletePrivacyRequest(w http.ResponseWriter, r *http.Request) {
	h.decidePrivacy(w, r, h.Privacy.Complete)
}

// RejectPrivacyRequest is POST /privacy/requests/{id}/reject {text}: the reason the person is told.
func (h *H) RejectPrivacyRequest(w http.ResponseWriter, r *http.Request) {
	h.decidePrivacy(w, r, h.Privacy.Reject)
}

func (h *H) decidePrivacy(w http.ResponseWriter, r *http.Request,
	decide func(ctx context.Context, id, by uuid.UUID, text string) (*ent.PrivacyRequest, error)) {
	req, ok := h.privacyRequest(w, r)
	if !ok {
		return
	}
	var in struct {
		Text string `json:"text"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := decide(r.Context(), req.ID, actor(r), in.Text)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// AnonymisePrivacyRequest is POST /privacy/requests/{id}/anonymise: completes an erasure.
func (h *H) AnonymisePrivacyRequest(w http.ResponseWriter, r *http.Request) {
	req, ok := h.privacyRequest(w, r)
	if !ok {
		return
	}
	out, err := h.Privacy.Anonymise(r.Context(), req.ID, actor(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// ExportPrivacyRequest is GET /privacy/requests/{id}/export: the person's data as a zip.
func (h *H) ExportPrivacyRequest(w http.ResponseWriter, r *http.Request) {
	req, ok := h.privacyRequest(w, r)
	if !ok {
		return
	}
	f, err := h.Privacy.Export(r.Context(), req.ID, false)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	sendFile(w, f)
}

// MyPrivacyRequests is GET /me/privacy-requests.
func (h *H) MyPrivacyRequests(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Privacy.ForParties(r.Context(), access(r).PartyIDs)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// MyCreatePrivacyRequest is POST /me/privacy-requests {kind, notes}: the signed-in person asks for
// a copy, a correction or erasure. It covers each of their records at this estate (one person can
// be on the register twice, say as owner and as buyer); records already erased or with the same
// request open are skipped.
func (h *H) MyCreatePrivacyRequest(w http.ResponseWriter, r *http.Request) {
	var in privacy.Input
	if !httpx.Decode(w, r, &in) {
		return
	}
	a := access(r)
	if len(a.PartyIDs) == 0 {
		httpx.Fail(w, httpx.Forbidden("only residents make requests here"))
		return
	}
	var (
		first   *ent.PrivacyRequest
		lastErr error
	)
	for _, pid := range a.PartyIDs {
		in.PartyID = pid
		req, err := h.Privacy.Create(r.Context(), in, "portal", a.AuthUserID)
		if err != nil {
			lastErr = err
			continue
		}
		if first == nil {
			first = req
		}
	}
	if first == nil {
		httpx.Fail(w, lastErr)
		return
	}
	httpx.JSON(w, http.StatusCreated, first)
}

// MyPrivacyExport is GET /me/privacy-requests/{id}/export, once the office has completed it.
func (h *H) MyPrivacyExport(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	req, err := h.Privacy.Get(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if req.PartyID == nil || !slices.Contains(access(r).PartyIDs, *req.PartyID) {
		httpx.Fail(w, httpx.Forbidden("this request is not yours"))
		return
	}
	f, err := h.Privacy.Export(r.Context(), req.ID, true)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	sendFile(w, f)
}
