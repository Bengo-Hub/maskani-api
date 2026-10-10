package handlers

import (
	"net/http"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/settings"
)

// ListApprovalRules is GET /settings/approval-rules.
func (h *H) ListApprovalRules(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Settings.ApprovalRules(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// CreateApprovalRule is POST /settings/approval-rules {action, min_amount, max_amount, levels, approver_roles, active}.
func (h *H) CreateApprovalRule(w http.ResponseWriter, r *http.Request) {
	var in settings.ApprovalRuleInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	rule, err := h.Settings.CreateApprovalRule(r.Context(), in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, rule)
}

// UpdateApprovalRule is PUT /settings/approval-rules/{id}.
func (h *H) UpdateApprovalRule(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	var in settings.ApprovalRuleInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	rule, err := h.Settings.UpdateApprovalRule(r.Context(), id, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, rule)
}

// DeleteApprovalRule is DELETE /settings/approval-rules/{id}.
func (h *H) DeleteApprovalRule(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	if err := h.Settings.DeleteApprovalRule(r.Context(), id); err != nil {
		httpx.Fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
