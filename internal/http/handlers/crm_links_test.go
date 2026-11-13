package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// A contact id that is not a UUID is refused before any lookup (S2-8 review M-12).
func TestCRMLinksRefusesABadContactID(t *testing.T) {
	for _, id := range []string{"not-a-uuid", "00000000-0000-0000-0000-000000000000"} {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("tenant", "acme")
		rctx.URLParams.Add("contactID", id)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		w := httptest.NewRecorder()
		CRMLinks(nil, nil)(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("contact %q: status %d, want 400", id, w.Code)
		}
	}
}
