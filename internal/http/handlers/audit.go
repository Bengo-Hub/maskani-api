package handlers

import (
	"net/http"
	"time"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/audit"
	"github.com/bengobox/maskani-api/internal/shared/page"
)

// ListAuditLogs is GET /audit-logs?target_type=&target_id=&actor_id=&from=&to= (keyset): the
// critical security workflows recorded for the tenant, newest first. from and to are dates.
func (h *H) ListAuditLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := audit.Filter{TargetType: q.Get("target_type"), TargetID: httpx.QueryUUID(r, "target_id"), ActorID: httpx.QueryUUID(r, "actor_id")}
	for _, d := range []struct {
		key string
		set func(time.Time)
		add int
	}{{"from", func(t time.Time) { f.From = &t }, 0}, {"to", func(t time.Time) { f.To = &t }, 1}} {
		if v := q.Get(d.key); v != "" {
			t, err := time.Parse("2006-01-02", v)
			if err != nil {
				httpx.Error(w, http.StatusBadRequest, "bad_request", d.key+" must be YYYY-MM-DD")
				return
			}
			d.set(t.AddDate(0, 0, d.add))
		}
	}
	res, err := h.Audit.List(r.Context(), access(r).TenantID, f, page.Parse(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}
