package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/modules/audit"
)

type memRecorder struct{ got []audit.Entry }

func (m *memRecorder) Record(_ context.Context, e audit.Entry) { m.got = append(m.got, e) }

func TestAuditRecordsOnlyCriticalWrites(t *testing.T) {
	rec := &memRecorder{}
	a := &Access{TenantID: uuid.New(), AuthUserID: uuid.New(), Email: "admin@example.com", Bypass: true}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(WithAccess(req.Context(), a)))
		})
	})
	r.Route("/api/v1/{tenant}/maskani", func(tr chi.Router) {
		tr.Use(Audit(rec))
		ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
		tr.Put("/users/{id}/roles", ok)
		tr.Post("/visitor-passes", ok)
		tr.Post("/manual-payments/{id}/approve", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusConflict) })
	})
	user := uuid.New()
	for _, c := range []struct{ method, path string }{
		{http.MethodPut, "/api/v1/demo/maskani/users/" + user.String() + "/roles"},                 // critical: recorded
		{http.MethodPost, "/api/v1/demo/maskani/visitor-passes"},                                   // routine: not recorded
		{http.MethodPost, "/api/v1/demo/maskani/manual-payments/" + uuid.NewString() + "/approve"}, // failed: not recorded
	} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(c.method, c.path, nil))
	}
	if len(rec.got) != 1 {
		t.Fatalf("want only the role change recorded, got %d: %+v", len(rec.got), rec.got)
	}
	e := rec.got[0]
	if e.Action != "user.roles_change" || e.TargetType != "user" || e.TargetID != user || e.ActorID != a.AuthUserID {
		t.Fatalf("entry: %+v", e)
	}
}
