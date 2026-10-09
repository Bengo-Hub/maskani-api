package handlers

import (
	"fmt"
	"net/http"

	"github.com/Bengo-Hub/reports"
	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/docs"
	analytics "github.com/bengobox/maskani-api/internal/modules/reports"
)

// StatementExport is GET /unit-accounts/{id}/statement/export?format=pdf|csv|xlsx (staff, property
// scoped): the branded statement with the account's full history.
func (h *H) StatementExport(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok || !h.accountScope(w, r, id) {
		return
	}
	h.statementExport(w, r, id)
}

// MyStatementExport is GET /me/accounts/{id}/statement/export?format=: the same document for the
// owner or occupant the account belongs to.
func (h *H) MyStatementExport(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	if err := h.Portal.OwnsAccount(r.Context(), access(r).PartyIDs, id); err != nil {
		httpx.Fail(w, err)
		return
	}
	h.statementExport(w, r, id)
}

func (h *H) statementExport(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	format, ok := exportFormat(w, r)
	if !ok {
		return
	}
	a := access(r)
	f, err := h.Docs.Statement(r.Context(), a.TenantID, a.TenantSlug, id, format)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	sendFile(w, f)
}

// ArrearsExport is GET /reports/arrears/export?format=&property_id=&q=&min=: every owing account in
// scope with the ageing breakdown.
func (h *H) ArrearsExport(w http.ResponseWriter, r *http.Request) {
	format, ok := exportFormat(w, r)
	if !ok {
		return
	}
	f, ok := scopeFilter(w, r)
	if !ok {
		return
	}
	a := access(r)
	file, err := h.Docs.Arrears(r.Context(), a.TenantID, a.TenantSlug, analytics.Scope{PropertyID: f.PropertyID, IDs: f.Scope, All: f.AllProperties},
		arrearsFilter(r), maskArrearsPhones(r), format)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	sendFile(w, file)
}

// PerformanceExport is GET /reports/insights/export?format=&property_id=&period=: the business view
// behind the staff dashboard as a document.
func (h *H) PerformanceExport(w http.ResponseWriter, r *http.Request) {
	format, ok := exportFormat(w, r)
	if !ok {
		return
	}
	f, ok := scopeFilter(w, r)
	if !ok {
		return
	}
	a := access(r)
	file, err := h.Docs.Performance(r.Context(), a.TenantID, a.TenantSlug, analytics.Scope{PropertyID: f.PropertyID, IDs: f.Scope, All: f.AllProperties},
		r.URL.Query().Get("period"), format)
	if err != nil {
		httpx.Fail(w, httpx.Invalid(err.Error()))
		return
	}
	sendFile(w, file)
}

// WaterBalanceExport is GET /water-balance/export?format=&property_id=&period=.
func (h *H) WaterBalanceExport(w http.ResponseWriter, r *http.Request) {
	format, ok := exportFormat(w, r)
	if !ok {
		return
	}
	pid := httpx.QueryUUID(r, "property_id")
	if pid == nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "property_id is required")
		return
	}
	if !requireProperty(w, r, *pid) {
		return
	}
	a := access(r)
	file, err := h.Docs.WaterBalance(r.Context(), a.TenantID, a.TenantSlug, *pid, period(r), format)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	sendFile(w, file)
}

func exportFormat(w http.ResponseWriter, r *http.Request) (reports.Format, bool) {
	format, err := reports.ParseFormat(r.URL.Query().Get("format"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", err.Error())
		return "", false
	}
	return format, true
}

// sendFile writes a document as a download. Statements hold personal and financial data, so no
// shared cache may keep them.
func sendFile(w http.ResponseWriter, f *docs.File) {
	w.Header().Set("Content-Type", f.Mime)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, f.Name))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(f.Body)
}
