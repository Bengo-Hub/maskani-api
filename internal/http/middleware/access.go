// Package middleware holds the per-request access resolution and gates used by every route.
package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Bengo-Hub/httpware"
	authclient "github.com/Bengo-Hub/shared-auth-client"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/maskaniuseroutlet"
	"github.com/bengobox/maskani-api/internal/ent/party"
	"github.com/bengobox/maskani-api/internal/ent/property"
	"github.com/bengobox/maskani-api/internal/modules/rbac"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// Access is everything a handler needs to authorise and scope a request.
type Access struct {
	TenantID   uuid.UUID
	TenantSlug string
	AuthUserID uuid.UUID
	LocalUser  *ent.MaskaniUser
	Email      string
	Roles      []string
	Perms      []string
	// Bypass is true for platform owners, superusers and S2S callers.
	Bypass bool
	// AllProperties is true when the caller is not limited to assigned properties.
	AllProperties bool
	// PropertyIDs are the properties a limited staff user may see.
	PropertyIDs []uuid.UUID
	// PartyIDs are the parties linked to a portal user.
	PartyIDs  []uuid.UUID
	IsService bool
	Claims    *authclient.Claims
}

type accessKey struct{}

// FromContext returns the request's Access (nil when the access middleware did not run).
func FromContext(ctx context.Context) *Access {
	a, _ := ctx.Value(accessKey{}).(*Access)
	return a
}

// IsStaff reports whether the caller holds any staff permission.
func (a *Access) IsStaff() bool { return a.Bypass || len(a.Perms) > 0 }

// Has reports whether the caller holds any of the permission codes.
func (a *Access) Has(codes ...string) bool {
	if a == nil {
		return false
	}
	if a.Bypass {
		return true
	}
	for _, c := range codes {
		for _, p := range a.Perms {
			if p == c {
				return true
			}
		}
		if a.Claims != nil && a.Claims.HasPermission(c) {
			return true
		}
	}
	return false
}

// CanSeeProperty reports whether the caller may see a property.
func (a *Access) CanSeeProperty(id uuid.UUID) bool {
	if a == nil {
		return false
	}
	if a.AllProperties {
		return true
	}
	for _, p := range a.PropertyIDs {
		if p == id {
			return true
		}
	}
	return false
}

// ResolveAccess builds Access after authentication and tenant resolution, provisions the local
// user, and scopes the request context with the tenant guard.
func ResolveAccess(client *ent.Client, rbacSvc *rbac.Service, log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			claims, ok := authclient.ClaimsFromContext(ctx)
			if !ok || claims == nil {
				WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
			tenantID, err := uuid.Parse(httpware.GetTenantID(ctx))
			if err != nil || tenantID == uuid.Nil {
				WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "tenant context required", "code": "tenant_required"})
				return
			}
			a := &Access{
				TenantID:   tenantID,
				TenantSlug: httpware.GetTenantSlug(ctx),
				Email:      claims.Email,
				IsService:  claims.IsService,
				Claims:     claims,
				Bypass:     claims.IsService || claims.IsPlatformOwner || claims.IsSuperuser(),
			}
			gctx := tenantguard.With(ctx, tenantID)

			if !claims.IsService {
				authUserID, uerr := claims.UserID()
				if uerr != nil {
					WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
					return
				}
				a.AuthUserID = authUserID
				user, eerr := rbacSvc.EnsureUser(gctx, rbac.Identity{
					TenantID: tenantID, AuthUserID: authUserID, Email: claims.Email, SSORoles: claims.Roles,
				})
				if eerr != nil {
					log.Warn("ensure user failed", zap.Error(eerr))
				}
				a.LocalUser = user
				a.Roles, a.Perms, _ = rbacSvc.Resolve(gctx, tenantID, authUserID)
				a.AllProperties = a.Bypass || claims.CanAccessAllOutlets() || containsStr(a.Roles, rbac.RoleTenantAdmin)
				if !a.AllProperties && user != nil {
					a.PropertyIDs, a.AllProperties = assignedProperties(gctx, client, tenantID, user.ID)
				}
				a.PartyIDs = linkedParties(gctx, client, authUserID)
			} else {
				a.AllProperties = true
			}

			ctx = context.WithValue(gctx, accessKey{}, a)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// assignedProperties returns the properties behind the user's outlet assignments. A staff user with
// no assignments is unrestricted until an admin assigns properties (the fleet's progressive rule).
func assignedProperties(ctx context.Context, client *ent.Client, tenantID, userID uuid.UUID) ([]uuid.UUID, bool) {
	rows, err := client.MaskaniUserOutlet.Query().
		Where(maskaniuseroutlet.TenantID(tenantID), maskaniuseroutlet.UserID(userID)).All(ctx)
	if err != nil || len(rows) == 0 {
		return nil, true
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, o := range rows {
		ids = append(ids, o.OutletID)
	}
	props, err := client.Property.Query().Where(property.OutletIDIn(ids...)).IDs(ctx)
	if err != nil {
		return nil, false
	}
	return props, false
}

func linkedParties(ctx context.Context, client *ent.Client, authUserID uuid.UUID) []uuid.UUID {
	ids, err := client.Party.Query().Where(party.AuthUserID(authUserID)).IDs(ctx)
	if err != nil {
		return nil
	}
	return ids
}

// RequirePermission allows the request when the caller holds any of the permissions.
func RequirePermission(codes ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if FromContext(r.Context()).Has(codes...) {
				next.ServeHTTP(w, r)
				return
			}
			WriteJSON(w, http.StatusForbidden, map[string]any{
				"error":    "you do not have permission to perform this action",
				"code":     "forbidden",
				"required": strings.Join(codes, " | "),
			})
		})
	}
}

// RequirePortalUser allows only callers linked to at least one party (owners, occupants, buyers).
func RequirePortalUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := FromContext(r.Context())
		if a == nil || len(a.PartyIDs) == 0 {
			WriteJSON(w, http.StatusForbidden, map[string]string{"error": "no units are linked to this account", "code": "not_linked"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// WriteJSON writes a JSON response.
func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func containsStr(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
