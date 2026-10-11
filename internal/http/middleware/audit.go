package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/audit"
)

// Recorder is the audit service as the middleware uses it.
type Recorder interface {
	Record(ctx context.Context, e audit.Entry)
}

// audited lists the only writes that enter the audit trail: critical security workflows (who has
// access, how the estate is configured, money decisions and changes of ownership). Routine work
// (passes, readings, requests, notices, payments through a gateway) is never logged here. The key
// is the method and the route below /api/v1/{tenant}/maskani; the value is the action recorded.
var audited = map[string]string{
	// Access and identity.
	"POST /roles":                           "role.create",
	"POST /roles/customize":                 "role.customize",
	"PUT /roles/{id}":                       "role.update",
	"DELETE /roles/{id}":                    "role.delete",
	"POST /users/invite":                    "user.invite",
	"PUT /users/{id}/roles":                 "user.roles_change",
	"PUT /users/{id}/status":                "user.status_change",
	"POST /properties/{id}/staff":           "property.staff_assign",
	"DELETE /staff-assignments/{id}":        "property.staff_remove",
	"POST /parties/{id}/invite":             "party.portal_invite",
	"POST /gate/devices":                    "gate.device_register",
	"POST /gate/devices/{id}/revoke":        "gate.device_revoke",
	"PUT /vendors/{id}/personnel/{pid}/pin": "vendor.personnel_pin_set",
	// Configuration.
	"PUT /settings":                           "settings.update",
	"PUT /settings/modules":                   "settings.modules_change",
	"POST /approvals/rules":                   "approval_rule.create",
	"PUT /approvals/rules/{id}":               "approval_rule.update",
	"DELETE /approvals/rules/{id}":            "approval_rule.delete",
	"PUT /document-sequences/{kind}":          "document.sequence_change",
	"POST /document-templates/{kind}/approve": "document.template_approve",
	"PATCH /funds/{id}":                       "fund.update",
	// Money decisions.
	"POST /approvals/{id}/approve":                 "approval.approve",
	"POST /approvals/{id}/reject":                  "approval.reject",
	"POST /manual-payments/{id}/approve":           "manual_payment.verify",
	"POST /manual-payments/{id}/reject":            "manual_payment.reject",
	"POST /unit-accounts/{id}/adjustments":         "adjustment.request",
	"POST /adjustments/{id}/approve":               "adjustment.approve",
	"POST /adjustments/{id}/reject":                "adjustment.reject",
	"POST /collections/suspense/{trans_id}/assign": "payment.suspense_assign",
	"POST /collections/bank-lines":                 "payment.bank_statement_import",
	"PUT /unit-accounts/{id}/payment-plan":         "payment_plan.agree",
	"DELETE /unit-accounts/{id}/payment-plan":      "payment_plan.cancel",
	"POST /billing-schedule/approve":               "billing.run_without_readings",
	// Ownership and the register.
	"POST /unit-parties/{id}/end": "unit.link_end",
	"POST /units/{id}/transfer":   "unit.transfer",
	"POST /imports/{id}/commit":   "register.import_commit",
	// Personal data: identity checks, a person's data leaving the system, and erasure.
	"POST /privacy/requests/{id}/verify":    "privacy.verify",
	"GET /privacy/requests/{id}/export":     "privacy.export",
	"POST /privacy/requests/{id}/anonymise": "privacy.erase",
	"POST /privacy/requests/{id}/reject":    "privacy.reject",
}

// AuditedRoutes lists the allowlisted "METHOD /route" keys, so the router test can check each one
// still matches a real route.
func AuditedRoutes() []string {
	out := make([]string, 0, len(audited))
	for k := range audited {
		out = append(out, k)
	}
	return out
}

// Audit records the critical security workflows in the allowlist above when they succeed: the
// action, the target (the first UUID in the route), the actor, IP and request id. It runs after the
// response is written, and a failed audit write is only logged, so it never slows or breaks a request.
func Audit(rec Recorder) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Reads pass through the same allowlist: only a listed one (a person's data export) is kept.
			if rec == nil || r.Method == http.MethodHead || r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}
			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			if ww.Status() < 200 || ww.Status() >= 300 {
				return
			}
			rc := chi.RouteContext(r.Context())
			if rc == nil {
				return
			}
			pattern := rc.RoutePattern()
			if i := strings.Index(pattern, "/maskani"); i >= 0 {
				pattern = pattern[i+len("/maskani"):]
			}
			action, ok := audited[r.Method+" "+pattern]
			if !ok {
				return
			}
			a := FromContext(r.Context())
			if a == nil || a.IsService {
				return
			}
			var target uuid.UUID
			for i, k := range rc.URLParams.Keys {
				if k == "tenant" {
					continue
				}
				if id, err := uuid.Parse(rc.URLParams.Values[i]); err == nil {
					target = id
					break
				}
			}
			ip := httpx.ClientIP(r)
			rec.Record(context.WithoutCancel(r.Context()), audit.Entry{TenantID: a.TenantID, ActorID: a.AuthUserID, ActorEmail: a.Email,
				Action: action, TargetType: strings.SplitN(action, ".", 2)[0], TargetID: target, IP: ip,
				RequestID: chimw.GetReqID(r.Context())})
		})
	}
}
