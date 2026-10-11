package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	authclient "github.com/Bengo-Hub/shared-auth-client"
)

func TestCheckPlanLimit(t *testing.T) {
	req := func(c *authclient.Claims) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/units", nil)
		return r.WithContext(authclient.ContextWithClaims(r.Context(), c))
	}
	tier1 := &authclient.Claims{SubscriptionLimits: map[string]int{"max_units": 150, "max_users": 10}}
	cases := []struct {
		name   string
		claims *authclient.Claims
		used   int
		add    int
		allow  bool
	}{
		{"room left", tier1, 149, 1, true},
		{"at the cap", tier1, 150, 1, false},
		{"an import that would cross it", tier1, 100, 60, false},
		{"unlimited", &authclient.Claims{SubscriptionLimits: map[string]int{"max_units": -1}}, 9000, 1, true},
		{"not set", &authclient.Claims{}, 9000, 1, true},
		{"exempt demo tenant", &authclient.Claims{IsDemo: true, SubscriptionLimits: map[string]int{"max_units": 150}}, 500, 1, true},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		if got := checkPlanLimit(w, req(c.claims), "units", "max_units", c.used, c.add); got != c.allow {
			t.Errorf("%s: allowed %v, want %v", c.name, got, c.allow)
		}
		if !c.allow && w.Code != http.StatusPaymentRequired {
			t.Errorf("%s: status %d, want 402", c.name, w.Code)
		}
	}
}
