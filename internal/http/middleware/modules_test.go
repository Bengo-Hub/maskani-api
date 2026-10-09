package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

type fixedModules map[string]bool

func (f fixedModules) Modules(context.Context, uuid.UUID) (map[string]bool, error) { return f, nil }

// TestModuleOffIsReadOnly checks FR-09: a switched-off module still answers reads (flagged read
// only) and refuses writes.
func TestModuleOffIsReadOnly(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := RequireModule(fixedModules{"billing": true}, "sales")(ok)
	acc := &Access{TenantID: uuid.New(), AuthUserID: uuid.New(), Perms: []string{"maskani.sales.view"}}

	get := httptest.NewRequest(http.MethodGet, "/sale-contracts", nil)
	get = get.WithContext(WithAccess(get.Context(), acc))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, get)
	if rec.Code != http.StatusOK || rec.Header().Get("X-Module-Read-Only") != "disabled" {
		t.Fatalf("read of a switched-off module: status %d, header %q", rec.Code, rec.Header().Get("X-Module-Read-Only"))
	}

	post := httptest.NewRequest(http.MethodPost, "/sale-contracts", nil)
	post = post.WithContext(WithAccess(post.Context(), acc))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, post)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("write to a switched-off module: status %d, want 403", rec.Code)
	}

	on := RequireModule(fixedModules{"sales": true}, "sales")(ok)
	rec = httptest.NewRecorder()
	on.ServeHTTP(rec, post)
	if rec.Code != http.StatusOK || rec.Header().Get("X-Module-Read-Only") != "" {
		t.Fatalf("write to an enabled module: status %d, header %q", rec.Code, rec.Header().Get("X-Module-Read-Only"))
	}
}
