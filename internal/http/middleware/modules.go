package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/modules/settings"
)

// ModuleChecker resolves a tenant's enabled modules, and a property's within them.
type ModuleChecker interface {
	Modules(ctx context.Context, tenantID uuid.UUID) (map[string]bool, error)
	PropertyModules(ctx context.Context, tenantID, propertyID uuid.UUID) (map[string]bool, error)
}

// maxPeekBody bounds how much of a JSON body is read to find its property_id.
const maxPeekBody = 1 << 20

// RequireModule allows changes only where the plan includes the module's feature, the tenant has
// switched the module on (SRDD 4.4) and, when the request names a property, that property's use
// case and overrides keep it on (modules by property, like POS outlets). Checks run on the server,
// so hiding a button is never the only control. Platform owners and S2S callers bypass.
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
			if err == nil && set[module] {
				if pid := requestProperty(r); pid != uuid.Nil {
					set, err = mc.PropertyModules(r.Context(), a.TenantID, pid)
					if err == nil && !set[module] {
						if read {
							w.Header().Set("X-Module-Read-Only", "property")
							next.ServeHTTP(w, r)
							return
						}
						WriteJSON(w, http.StatusForbidden, map[string]string{
							"error": "this module is not used at this property", "code": "module_not_enabled_for_property", "module": module,
						})
						return
					}
				}
			}
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

// requestProperty finds the property a request is about: the property_id query parameter, or the
// property_id of a JSON body (read up to 1 MB and put back for the handler). Requests about one
// record (a work order by id) carry no property here and are checked at tenant level.
func requestProperty(r *http.Request) uuid.UUID {
	if v := r.URL.Query().Get("property_id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			return id
		}
	}
	if r.Body == nil || r.Method == http.MethodGet || r.Method == http.MethodHead ||
		!strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return uuid.Nil
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxPeekBody+1))
	rest := r.Body
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(raw), rest), rest}
	if err != nil || len(raw) > maxPeekBody {
		return uuid.Nil
	}
	var probe struct {
		PropertyID string `json:"property_id"`
	}
	if json.Unmarshal(raw, &probe) != nil {
		return uuid.Nil
	}
	id, _ := uuid.Parse(probe.PropertyID)
	return id
}
