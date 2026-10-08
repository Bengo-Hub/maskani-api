package handlers

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/notices"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

func uuidList(v string) ([]uuid.UUID, bool) {
	if v == "" {
		return nil, true
	}
	var out []uuid.UUID
	for _, s := range strings.Split(v, ",") {
		id, err := uuid.Parse(strings.TrimSpace(s))
		if err != nil {
			return nil, false
		}
		out = append(out, id)
	}
	return out, true
}

// InternalResidentsReach is GET /api/v1/internal/residents/reach (internal service key): one page of
// an estate audience for notifications-api's maskani_residents resolver. Query: tenant_id
// (required), property_id, block_ids, unit_ids, roles (comma separated), after, limit (max 500).
func (h *H) InternalResidentsReach(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	tenantID, err := uuid.Parse(q.Get("tenant_id"))
	if err != nil {
		httpx.Fail(w, httpx.Invalid("tenant_id is required"))
		return
	}
	a := notices.Audience{}
	if v := q.Get("property_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			httpx.Fail(w, httpx.Invalid("property_id is not valid"))
			return
		}
		a.PropertyID = &id
	}
	var ok bool
	if a.BlockIDs, ok = uuidList(q.Get("block_ids")); !ok {
		httpx.Fail(w, httpx.Invalid("block_ids are not valid"))
		return
	}
	if a.UnitIDs, ok = uuidList(q.Get("unit_ids")); !ok {
		httpx.Fail(w, httpx.Invalid("unit_ids are not valid"))
		return
	}
	for _, role := range strings.Split(q.Get("roles"), ",") {
		if role = strings.TrimSpace(role); role != "" {
			a.Roles = append(a.Roles, role)
		}
	}
	ctx := tenantguard.With(r.Context(), tenantID)
	rows, next, err := h.Notices.Reach(ctx, a, q.Get("after"), intQuery(r, "limit", 200))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows, "next": next})
}
