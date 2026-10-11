package router

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/bengobox/maskani-api/internal/http/handlers"
	mw "github.com/bengobox/maskani-api/internal/http/middleware"
)

// TestAuditedRoutesExist keeps the audit allowlist honest: every critical workflow it names must
// still be a real route, so renaming a route can never silently drop it from the audit trail.
func TestAuditedRoutesExist(t *testing.T) {
	r := chi.NewRouter()
	mount(r, Deps{H: &handlers.H{}})
	have := map[string]bool{}
	if err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		have[method+" "+route] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, k := range mw.AuditedRoutes() {
		if !have[k] {
			t.Errorf("audited route %q is not mounted", k)
		}
	}
}
