package handlers

import (
	"net/http"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	mw "github.com/bengobox/maskani-api/internal/http/middleware"
	"github.com/bengobox/maskani-api/internal/modules/erp"
)

// requireERP lets a request read from or write to ERP only when the tenant's plan includes it
// (erp.Feature). Otherwise it writes the structured 403 the console shows as an upgrade prompt:
// nothing is fetched from erp and no ERP link is saved until the tenant upgrades. Platform owners
// and S2S callers pass, as at the module gate.
func requireERP(w http.ResponseWriter, r *http.Request) bool {
	a := access(r)
	if a != nil && (a.Bypass || erp.Allowed(a.Claims)) {
		return true
	}
	mw.WriteJSON(w, http.StatusForbidden, map[string]any{
		"error":   "Staff from ERP are not included in your plan. Upgrade the plan to pick ERP staff.",
		"code":    "feature_not_available",
		"feature": erp.Feature,
		"upgrade": true,
	})
	return false
}

// ERPEmployees is GET /erp/employees?q=: the tenant's active ERP staff for the work order and
// staff pickers (id, staff number, name, email), at most 50.
func (h *H) ERPEmployees(w http.ResponseWriter, r *http.Request) {
	if !requireERP(w, r) {
		return
	}
	if !h.ERP.Enabled() {
		httpx.JSON(w, http.StatusServiceUnavailable, map[string]string{"error": "ERP is not connected"})
		return
	}
	rows, err := h.ERP.Employees(r.Context(), access(r).TenantID, r.URL.Query().Get("q"), 50)
	if err != nil {
		httpx.JSON(w, http.StatusBadGateway, map[string]string{"error": "could not reach ERP; try again"})
		return
	}
	if rows == nil {
		rows = []erp.Employee{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}
