package handlers

import (
	"net/http"
	"slices"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/docs"
)

// staffDocument loads a document for the office and checks the caller's property scope.
func (h *H) staffDocument(w http.ResponseWriter, r *http.Request) (*ent.Document, bool) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return nil, false
	}
	d, err := h.Docs.Document(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return nil, false
	}
	return d, requireProperty(w, r, docs.PropertyOf(d))
}

// SendDocumentCode is POST /documents/{id}/send-code {party_id}: a code to the phone and email on
// the person's record, for accepting the agreement at the office.
func (h *H) SendDocumentCode(w http.ResponseWriter, r *http.Request) {
	d, ok := h.staffDocument(w, r)
	if !ok {
		return
	}
	var in struct {
		PartyID uuid.UUID `json:"party_id"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	if err := h.Docs.SendAcceptanceCode(r.Context(), d.ID, in.PartyID); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

// AcceptDocumentAtOffice is POST /documents/{id}/accept {party_id, code}: the person reads the code
// back at the office.
func (h *H) AcceptDocumentAtOffice(w http.ResponseWriter, r *http.Request) {
	d, ok := h.staffDocument(w, r)
	if !ok {
		return
	}
	var in struct {
		PartyID uuid.UUID `json:"party_id"`
		Code    string    `json:"code"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := h.Docs.Accept(r.Context(), d.ID, docs.Acceptance{PartyID: in.PartyID, Code: in.Code, Via: "office",
		IP: httpx.ClientIP(r), UserAgent: r.UserAgent()})
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// RecordSignedCopy is POST /documents/{id}/signed-copy {keys}: photos of the signed paper copy,
// uploaded first through /media/upload with kind documents.
func (h *H) RecordSignedCopy(w http.ResponseWriter, r *http.Request) {
	d, ok := h.staffDocument(w, r)
	if !ok {
		return
	}
	var in struct {
		Keys []string `json:"keys"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := h.Docs.RecordWetSigned(r.Context(), d.ID, actor(r), in.Keys)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// DocumentSignatures is GET /documents/{id}/signatures: acceptances and signed copies.
func (h *H) DocumentSignatures(w http.ResponseWriter, r *http.Request) {
	d, ok := h.staffDocument(w, r)
	if !ok {
		return
	}
	rows, err := h.Docs.Signatures(r.Context(), d.ID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// portalDocument loads a document addressed to the signed-in person and returns their party on it.
func (h *H) portalDocument(w http.ResponseWriter, r *http.Request) (*ent.Document, uuid.UUID, bool) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return nil, uuid.Nil, false
	}
	d, err := h.Docs.Document(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return nil, uuid.Nil, false
	}
	for _, p := range access(r).PartyIDs {
		if slices.Contains(d.PartyIds, p.String()) && d.Status != "draft" {
			return d, p, true
		}
	}
	httpx.Error(w, http.StatusNotFound, "not_found", "not found")
	return nil, uuid.Nil, false
}

// MySendDocumentCode is POST /me/documents/{id}/send-code: a code to accept the agreement.
func (h *H) MySendDocumentCode(w http.ResponseWriter, r *http.Request) {
	d, pid, ok := h.portalDocument(w, r)
	if !ok {
		return
	}
	if err := h.Docs.SendAcceptanceCode(r.Context(), d.ID, pid); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

// MyAcceptDocument is POST /me/documents/{id}/accept {code}.
func (h *H) MyAcceptDocument(w http.ResponseWriter, r *http.Request) {
	d, pid, ok := h.portalDocument(w, r)
	if !ok {
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := h.Docs.Accept(r.Context(), d.ID, docs.Acceptance{PartyID: pid, Code: in.Code, Via: "portal",
		IP: httpx.ClientIP(r), UserAgent: r.UserAgent()})
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}
