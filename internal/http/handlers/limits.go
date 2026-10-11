package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	authclient "github.com/Bengo-Hub/shared-auth-client"
)

// Plan limits (SRDD wave 2.4): the subscription tier caps units under management (max_units) and
// staff users (max_users); owners, occupants, guards and vendor supervisors never count. The caps
// ride on the token, the same as pos-api's structural limits, and exempt tenants (platform owner,
// demo, subscription exempt) are never blocked.

// checkPlanLimit reports whether adding `adding` more of a metric keeps the tenant within its plan.
// When it would not, it writes the structured 402 the console shows as an upgrade prompt.
func checkPlanLimit(w http.ResponseWriter, r *http.Request, metric, limitKey string, used, adding int) bool {
	claims, ok := authclient.ClaimsFromContext(r.Context())
	if !ok || claims.IsGatingExempt() {
		return true
	}
	limit := claims.GetLimit(limitKey)
	if limit <= 0 || used+adding <= limit {
		return true // 0 not configured, -1 unlimited
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusPaymentRequired)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code": "usage_limit_exceeded", "error": "usage_limit_exceeded",
		"message": fmt.Sprintf("Your plan covers %d %s and you have %d. Upgrade the plan to add more.", limit, metric, used),
		"metric":  metric, "limit": limit, "used": used, "overage_eligible": false,
	})
	return false
}

// unitLimit checks room for `adding` more units.
func (h *H) unitLimit(w http.ResponseWriter, r *http.Request, adding int) bool {
	n, err := h.Register.CountUnits(r.Context())
	if err != nil {
		return true // never block on a counting hiccup; the next attempt checks again
	}
	return checkPlanLimit(w, r, "units", "max_units", n, adding)
}

// staffLimit checks room for one more staff user.
func (h *H) staffLimit(w http.ResponseWriter, r *http.Request) bool {
	n, err := h.RBAC.CountStaff(r.Context(), access(r).TenantID)
	if err != nil {
		return true
	}
	return checkPlanLimit(w, r, "staff users", "max_users", n, 1)
}
