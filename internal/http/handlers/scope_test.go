package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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

// TestScopeOnWriteRoutes covers write routes that carry the property in the body: a staff user of
// one property cannot act on another, and only all-property staff can send an estate-wide notice.
func TestScopeOnWriteRoutes(t *testing.T) {
	mine, other := uuid.New(), uuid.New()
	h := &H{}
	acc := &mw.Access{TenantID: uuid.New(), AuthUserID: uuid.New(), Perms: []string{"maskani.notices.manage", "maskani.gate.manage",
		"maskani.users.manage"}, PropertyIDs: []uuid.UUID{mine}}
	cases := map[string]struct {
		fn   http.HandlerFunc
		body string
		want int
	}{
		"incident on another property": {h.ReportIncident, `{"property_id":"` + other.String() + `","category":"other","severity":"low","title":"x"}`, http.StatusForbidden},
		"notice for another property":  {h.CreateNotice, `{"property_id":"` + other.String() + `","title":"x"}`, http.StatusForbidden},
		"estate-wide notice":           {h.CreateNotice, `{"title":"x"}`, http.StatusForbidden},
		"invite with no property":      {h.InviteStaff, `{"email":"a@b.co","name":"A","roles":["caretaker"]}`, http.StatusUnprocessableEntity},
		"invite into another property": {h.InviteStaff, `{"email":"a@b.co","name":"A","roles":["caretaker"],"property_ids":["` + other.String() + `"]}`, http.StatusForbidden},
	}
	for name, c := range cases {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(c.body))
		req = req.WithContext(mw.WithAccess(req.Context(), acc))
		rec := httptest.NewRecorder()
		c.fn(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s: status %d, want %d (%s)", name, rec.Code, c.want, rec.Body.String())
		}
	}

	// Meters of another property, read side.
	req := httptest.NewRequest(http.MethodGet, "/meters?property_id="+other.String(), nil)
	req = req.WithContext(mw.WithAccess(req.Context(), acc))
	rec := httptest.NewRecorder()
	h.ListMeters(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("meters of another property: status %d, want 403", rec.Code)
	}
}

// TestHoldsAllBlocksEscalation checks nobody can write a permission they lack into a role.
func TestHoldsAllBlocksEscalation(t *testing.T) {
	acc := &mw.Access{TenantID: uuid.New(), AuthUserID: uuid.New(), Perms: []string{"maskani.users.manage", "maskani.units.view"}}
	req := httptest.NewRequest(http.MethodPost, "/roles", nil)
	req = req.WithContext(mw.WithAccess(req.Context(), acc))
	rec := httptest.NewRecorder()
	if holdsAll(rec, req, []string{"maskani.units.view", "maskani.tenant.admin"}) {
		t.Fatal("holdsAll allowed a permission the caller does not hold")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", rec.Code)
	}
	rec = httptest.NewRecorder()
	if !holdsAll(rec, req, []string{"maskani.units.view"}) {
		t.Fatal("holdsAll refused a permission the caller holds")
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
