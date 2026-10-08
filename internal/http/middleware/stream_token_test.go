package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStreamQueryToken(t *testing.T) {
	var gotAuth, gotQuery string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotQuery = r.Header.Get("Authorization"), r.URL.RawQuery
	})
	h := StreamQueryToken(next)

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/acme/maskani/stream?token=abc&x=1", nil))
	if gotAuth != "Bearer abc" || gotQuery != "x=1" {
		t.Fatalf("stream: auth %q query %q", gotAuth, gotQuery)
	}

	req := httptest.NewRequest("GET", "/api/v1/acme/maskani/stream?token=abc", nil)
	req.Header.Set("Authorization", "Bearer header")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if gotAuth != "Bearer header" {
		t.Fatalf("an existing header must win, got %q", gotAuth)
	}

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/acme/maskani/units?token=abc", nil))
	if gotAuth != "" || gotQuery != "token=abc" {
		t.Fatalf("other routes must ignore ?token=: auth %q query %q", gotAuth, gotQuery)
	}
}
