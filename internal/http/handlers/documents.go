package handlers

import (
	"net"
	"net/http"
	"slices"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/docs"
)

// DocumentTemplates is GET /document-templates: per kind, the template in use (the estate's
// approved version or the Codevertex starter) and any draft, with the merge fields it may use.
func (h *H) DocumentTemplates(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Docs.Templates(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows, "kinds": docs.DocKinds})
}

// SaveDocumentTemplate is PUT /document-templates/{kind} {name, body}: new wording as a draft.
func (h *H) SaveDocumentTemplate(w http.ResponseWriter, r *http.Request) {
	var in docs.TemplateInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	t, err := h.Docs.SaveDraft(r.Context(), chiParam(r, "kind"), in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, t)
}

// ApproveDocumentTemplate is POST /document-templates/{kind}/approve {version}: puts a draft in use.
func (h *H) ApproveDocumentTemplate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Version int `json:"version"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	t, err := h.Docs.Approve(r.Context(), actor(r), chiParam(r, "kind"), in.Version)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, t)
}

// IssueDocument is POST /documents {kind, entity_id, values}: renders, numbers and stores a
// document about an account or contract at a property the caller can see.
func (h *H) IssueDocument(w http.ResponseWriter, r *http.Request) {
	var in docs.IssueInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	k, ok := docs.DocKindOf(in.Kind)
	if !ok {
		httpx.Fail(w, httpx.Invalid("unknown document kind"))
		return
	}
	sub, err := h.Docs.Subject(r.Context(), k, in.EntityID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if !requireProperty(w, r, sub.PropertyID) {
		return
	}
	d, err := h.Docs.Issue(r.Context(), actor(r), access(r).TenantSlug, k, in.EntityID, sub, in.Values)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, d)
}

// ListDocuments is GET /documents?unit_id= or ?entity_type=&entity_id=: newest first, limited to
// the properties the caller can see.
func (h *H) ListDocuments(w http.ResponseWriter, r *http.Request) {
	var rows []*ent.Document
	var err error
	if uid := httpx.QueryUUID(r, "unit_id"); uid != nil {
		u, uerr := h.Register.GetUnit(r.Context(), *uid)
		if uerr != nil {
			httpx.Fail(w, uerr)
			return
		}
		if !requireProperty(w, r, u.PropertyID) {
			return
		}
		rows, err = h.Docs.UnitDocuments(r.Context(), *uid)
	} else if eid := httpx.QueryUUID(r, "entity_id"); eid != nil {
		rows, err = h.Docs.Documents(r.Context(), r.URL.Query().Get("entity_type"), *eid)
	} else {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "unit_id or entity_id is required")
		return
	}
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	a := access(r)
	out := make([]*ent.Document, 0, len(rows))
	for _, d := range rows {
		if a.AllProperties || a.Bypass || slices.Contains(a.PropertyIDs, docs.PropertyOf(d)) {
			out = append(out, d)
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

// DocumentFile is GET /documents/{id}/file: the stored PDF; the download is logged.
func (h *H) DocumentFile(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	d, err := h.Docs.Document(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if !requireProperty(w, r, docs.PropertyOf(d)) {
		return
	}
	who := actor(r)
	h.sendDocument(w, r, d, &who, "staff")
}

// MyDocuments is GET /me/documents: documents addressed to the signed-in owner or resident.
func (h *H) MyDocuments(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Docs.PartyDocuments(r.Context(), access(r).PartyIDs)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// MyDocumentFile is GET /me/documents/{id}/file: a document addressed to one of the caller's parties.
func (h *H) MyDocumentFile(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	d, err := h.Docs.Document(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	mine := false
	for _, p := range access(r).PartyIDs {
		if slices.Contains(d.PartyIds, p.String()) {
			mine = true
			break
		}
	}
	if !mine || d.Status == "draft" {
		httpx.Error(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	who := actor(r)
	h.sendDocument(w, r, d, &who, "portal")
}

func (h *H) sendDocument(w http.ResponseWriter, r *http.Request, d *ent.Document, who *uuid.UUID, kind string) {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	f, err := h.Docs.File(r.Context(), d, who, kind, ip)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	sendFile(w, f)
}

// VerifyDocument is GET /api/v1/public/documents/verify/{code}: anyone holding a document checks
// it is genuine. Only the number, title, status, issue date, issuer and file hash are returned.
func (h *H) VerifyDocument(w http.ResponseWriter, r *http.Request) {
	v, err := h.Docs.Verify(r.Context(), chiParam(r, "code"))
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "not_found", "no document has this code")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusOK, v)
}
