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

// RequireModule allows the request only where the plan includes the module's feature and the
// tenant has switched the module on (SRDD 4.4). Checks run on the server, so hiding a button is
// never the only control. Platform owners and S2S callers bypass.
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
			if a.Claims != nil && !a.Claims.FeatureEnabled(settings.FeatureCode(module)) {
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
				WriteJSON(w, http.StatusForbidden, map[string]string{
					"error": "this module is not enabled", "code": "module_not_enabled", "module": module,
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
