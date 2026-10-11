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

// ReportExport is GET /reports/{id}/export?format=&from=&to=&period=&property_id=&q=&min=: any
// report in the registry as PDF, Excel or CSV through the one branded docs engine.
func (h *H) ReportExport(w http.ResponseWriter, r *http.Request) {
	h.exportReport(w, r, chiParam(r, "id"))
}

// ArrearsExport is GET /reports/arrears/export, kept for the Collections page; same as ReportExport.
func (h *H) ArrearsExport(w http.ResponseWriter, r *http.Request) { h.exportReport(w, r, "arrears") }

// PerformanceExport is GET /reports/insights/export, kept for the dashboard; same as ReportExport.
func (h *H) PerformanceExport(w http.ResponseWriter, r *http.Request) {
	h.exportReport(w, r, "performance")
}

func (h *H) exportReport(w http.ResponseWriter, r *http.Request, id string) {
	format, ok := exportFormat(w, r)
	if !ok {
		return
	}
	// The dashboard's filters: the performance report follows them; the others read from and to.
	sc, df, ok := h.dashboardFilter(w, r)
	if !ok {
		return
	}
	a := access(r)
	q := r.URL.Query()
	file, err := h.Docs.Export(r.Context(), a.TenantID, a.TenantSlug, sc, id,
		docs.ExportParams{Range: analytics.ReportRange{From: q.Get("from"), To: q.Get("to")}, Dashboard: df,
			Arrears: arrearsFilter(r), MaskPhones: maskArrearsPhones(r)}, format)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	sendFile(w, file)
}

// WaterBalanceExport is GET /water-balance/export?format=&property_id=&period=, kept for the water
// page: the registry's water report for the twelve months ending at the period.
func (h *H) WaterBalanceExport(w http.ResponseWriter, r *http.Request) {
	pid := httpx.QueryUUID(r, "property_id")
	if pid == nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "property_id is required")
		return
	}
	if !requireProperty(w, r, *pid) {
		return
	}
	q := r.URL.Query()
	if q.Get("to") == "" {
		q.Set("to", period(r))
		r.URL.RawQuery = q.Encode()
	}
	h.exportReport(w, r, "water")
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
