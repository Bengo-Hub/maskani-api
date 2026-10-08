package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	mw "github.com/bengobox/maskani-api/internal/http/middleware"
)

// TestPropertyScopeRejectsOtherProperties checks that a staff user limited to one property gets
// 403 on every list and report route when asking for another property. The handlers reject before
// touching a service, so no database is needed (the tenant guard itself is covered by
// tenantguard.TestCrossTenantIsolation).
func TestPropertyScopeRejectsOtherProperties(t *testing.T) {
	mine, other := uuid.New(), uuid.New()
	h := &H{}
	acc := &mw.Access{TenantID: uuid.New(), AuthUserID: uuid.New(), Perms: []string{"maskani.reports.view"},
		PropertyIDs: []uuid.UUID{mine}}
	routes := map[string]http.HandlerFunc{
		"/price-lists":            h.ListPriceLists,
		"/sale-contracts":         h.ListContracts,
		"/reservations":           h.ListReservations,
		"/visitor-passes":         h.ListPasses,
		"/incidents":              h.ListIncidents,
		"/reports/arrears":        h.Arrears,
		"/reports/dashboard":      h.Dashboard,
		"/reports/sales-position": h.SalesPosition,
		"/unit-accounts":          h.ListAccounts,
		"/billing-runs":           h.ListRuns,
		"/notices":                h.ListNotices,
		"/enquiries":              h.ListEnquiries,
		"/availability":           h.Availability,
		"/water-balance":          h.WaterBalance,
	}
	for path, fn := range routes {
		req := httptest.NewRequest(http.MethodGet, path+"?property_id="+other.String(), nil)
		req = req.WithContext(mw.WithAccess(req.Context(), acc))
		rec := httptest.NewRecorder()
		fn(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s with another property: status %d, want 403", path, rec.Code)
		}
	}

	// Path-parameter routes: staff of another property.
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", other.String())
	req := httptest.NewRequest(http.MethodGet, "/properties/"+other.String()+"/staff", nil)
	req = req.WithContext(context.WithValue(mw.WithAccess(req.Context(), acc), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.ListStaff(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("staff of another property: status %d, want 403", rec.Code)
	}
}

func TestStreamDisabledWithoutHub(t *testing.T) {
	h := &H{}
	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	req = req.WithContext(mw.WithAccess(req.Context(), &mw.Access{TenantID: uuid.New(), AllProperties: true, Bypass: true}))
	rec := httptest.NewRecorder()
	h.Stream(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", rec.Code)
	}
}
