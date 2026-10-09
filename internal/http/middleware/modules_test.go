package middleware

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type fixedModules map[string]bool

func (f fixedModules) Modules(context.Context, uuid.UUID) (map[string]bool, error) { return f, nil }

// PropertyModules: the property named "narrow" has only billing; any other has the tenant's set.
func (f fixedModules) PropertyModules(_ context.Context, _, pid uuid.UUID) (map[string]bool, error) {
	if pid == narrowProperty {
		return map[string]bool{"billing": true}, nil
	}
	return f, nil
}

var narrowProperty = uuid.MustParse("11111111-1111-1111-1111-111111111111")

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

// TestModuleOffAtProperty checks modules by property: a property whose use case leaves sales out
// reads sales as read only and refuses a sales write named for it in the query or the JSON body,
// while the handler still receives the whole body.
func TestModuleOffAtProperty(t *testing.T) {
	var got string
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		w.WriteHeader(http.StatusOK)
	})
	h := RequireModule(fixedModules{"billing": true, "sales": true}, "sales")(ok)
	acc := &Access{TenantID: uuid.New(), AuthUserID: uuid.New()}
	send := func(method, url, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, url, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		req = req.WithContext(WithAccess(req.Context(), acc))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := send(http.MethodGet, "/sale-contracts?property_id="+narrowProperty.String(), ""); rec.Code != http.StatusOK || rec.Header().Get("X-Module-Read-Only") != "property" {
		t.Fatalf("read at a property without sales: status %d, header %q", rec.Code, rec.Header().Get("X-Module-Read-Only"))
	}
	if rec := send(http.MethodPost, "/reservations", `{"property_id":"`+narrowProperty.String()+`"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("write at a property without sales: status %d, want 403", rec.Code)
	}
	other := `{"property_id":"` + uuid.NewString() + `","unit_id":"x"}`
	if rec := send(http.MethodPost, "/reservations", other); rec.Code != http.StatusOK {
		t.Fatalf("write at a property with sales: status %d, want 200", rec.Code)
	}
	if got != other {
		t.Fatalf("handler body after the peek: %q, want %q", got, other)
	}
}
