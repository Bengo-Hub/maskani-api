package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/modules/settings"
)

// ModuleChecker resolves a tenant's enabled modules.
type ModuleChecker interface {
	Modules(ctx context.Context, tenantID uuid.UUID) (map[string]bool, error)
}

// RequireModule allows changes only where the plan includes the module's feature and the tenant
// has switched the module on (SRDD 4.4). Checks run on the server, so hiding a button is never the
// only control. Platform owners and S2S callers bypass.
//
// Reads stay open when a module is switched off or the plan no longer covers it: the data is kept
// read only and exportable (FR-09, SRDD 4.8), and the response carries X-Module-Read-Only so the
// screen can say why nothing can be changed. Route permissions still apply to every read.
func RequireModule(mc ModuleChecker, module string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a := FromContext(r.Context())
			if a == nil {
				WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
			if a.Bypass {
				next.ServeHTTP(w, r)
				return
			}
			read := r.Method == http.MethodGet || r.Method == http.MethodHead
			if a.Claims != nil && !a.Claims.FeatureEnabled(settings.FeatureCode(module)) {
				if read {
					w.Header().Set("X-Module-Read-Only", "plan")
					next.ServeHTTP(w, r)
					return
				}
				WriteJSON(w, http.StatusForbidden, map[string]string{
					"error": "your plan does not include this module", "code": "feature_not_available",
					"feature": settings.FeatureCode(module),
				})
				return
			}
			set, err := mc.Modules(r.Context(), a.TenantID)
			if err != nil {
				WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "could not check modules"})
				return
			}
			if !set[module] {
				if read {
					w.Header().Set("X-Module-Read-Only", "disabled")
					next.ServeHTTP(w, r)
					return
				}
				WriteJSON(w, http.StatusForbidden, map[string]string{
					"error": "this module is not enabled", "code": "module_not_enabled", "module": module,
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
