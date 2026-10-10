package handlers

import (
	"encoding/json"
	"net/http"
	"slices"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/approvals"
	"github.com/bengobox/maskani-api/internal/modules/rbac"
	"github.com/bengobox/maskani-api/internal/shared/page"
)

// approver is the caller as the approvals engine sees them: role codes, a permission check, and
// administrators (tenant admins, platform owners) who may act on any step.
func approver(r *http.Request) approvals.Actor {
	a := access(r)
	return approvals.Actor{UserID: actor(r), Name: displayName(r), Roles: a.Roles, HasPerm: func(p string) bool { return a.Has(p) },
		Admin: a.Bypass || slices.Contains(a.Roles, rbac.RoleTenantAdmin)}
}

// approvalPerms are the permission steps the built-in defaults use, so "waiting for me" can match them.
var approvalPerms = []string{rbac.PermBillingApprove, rbac.PermBillingVerify}

// ListApprovals is GET /approvals?status=&module=&property_id=&mine=true (keyset): the central
// inbox across every workflow. mine keeps requests whose current step the caller may act on.
func (h *H) ListApprovals(w http.ResponseWriter, r *http.Request) {
	ids, all, ok := scopeIDs(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := approvals.Filter{Status: q.Get("status"), Module: q.Get("module"), PropertyIDs: ids, All: all}
	switch f.Status {
	case "", "pending", "approved", "rejected", "cancelled":
	default:
		httpx.Error(w, http.StatusBadRequest, "bad_request", "status must be pending, approved, rejected or cancelled")
		return
	}
	if q.Get("mine") == "true" {
		me := approver(r)
		if !me.Admin {
			var perms []string
			for _, p := range approvalPerms {
				if me.HasPerm(p) {
					perms = append(perms, p)
				}
			}
			f.Approvers = me.Approvers(perms)
		}
	}
	res, err := h.Approvals.List(r.Context(), f, page.Parse(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

func (h *H) decide(w http.ResponseWriter, r *http.Request, d approvals.Decision) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	req, err := h.Approvals.Get(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if req.PropertyID != nil && !requireProperty(w, r, *req.PropertyID) {
		return
	}
	var in struct {
		Comment string `json:"comment"`
	}
	if d == approvals.Reject && !httpx.Decode(w, r, &in) {
		return
	}
	if d == approvals.Approve {
		_ = json.NewDecoder(r.Body).Decode(&in) // the comment is optional on approve
	}
	out, err := h.Approvals.Decide(r.Context(), id, approver(r), d, in.Comment)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	h.Reports.Invalidate(access(r).TenantID)
	httpx.JSON(w, http.StatusOK, out)
}

// ApproveRequest is POST /approvals/{id}/approve {comment}: approves the current step; the last
// step runs the workflow's own effect (book the payment, raise the credit note).
func (h *H) ApproveRequest(w http.ResponseWriter, r *http.Request) { h.decide(w, r, approvals.Approve) }

// RejectRequest is POST /approvals/{id}/reject {comment}: the comment is required.
func (h *H) RejectRequest(w http.ResponseWriter, r *http.Request) { h.decide(w, r, approvals.Reject) }

// ListApprovalRules is GET /approvals/rules.
func (h *H) ListApprovalRules(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Approvals.Rules(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// CreateApprovalRule is POST /approvals/rules {module, name, min_amount, max_amount, steps: [{name, approver_role}], is_active}.
func (h *H) CreateApprovalRule(w http.ResponseWriter, r *http.Request) {
	var in approvals.RuleInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	rule, err := h.Approvals.CreateRule(r.Context(), in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, rule)
}

// UpdateApprovalRule is PUT /approvals/rules/{id}.
func (h *H) UpdateApprovalRule(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	var in approvals.RuleInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	rule, err := h.Approvals.UpdateRule(r.Context(), id, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, rule)
}

// DeleteApprovalRule is DELETE /approvals/rules/{id}.
func (h *H) DeleteApprovalRule(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	if err := h.Approvals.DeleteRule(r.Context(), id); err != nil {
		httpx.Fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// withApprovals writes a keyset page with each row's latest approval request beside it
// ({data, next_cursor, has_more, approvals: {object_id: request}}).
func (h *H) withApprovals(w http.ResponseWriter, r *http.Request, res any, objectIDs []uuid.UUID) {
	reqs, err := h.Approvals.ForObjects(r.Context(), objectIDs)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	b, _ := json.Marshal(res)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	out["approvals"] = reqs
	httpx.JSON(w, http.StatusOK, out)
}
