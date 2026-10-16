package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authclient "github.com/Bengo-Hub/shared-auth-client"

	mw "github.com/bengobox/maskani-api/internal/http/middleware"
	"github.com/bengobox/maskani-api/internal/modules/erp"
)

// A tenant whose plan has no ERP gets nothing from erp and saves no ERP link: the picker and the
// assign paths answer 403 feature_not_available with the upgrade flag. Exempt tenants, platform
// owners and S2S callers pass.
func TestRequireERP(t *testing.T) {
	cases := []struct {
		name  string
		a     *mw.Access
		allow bool
	}{
		{"plan with ERP", &mw.Access{Claims: &authclient.Claims{SubscriptionFeatures: []string{"maskani_maintenance", erp.Feature}}}, true},
		{"plan without ERP", &mw.Access{Claims: &authclient.Claims{SubscriptionFeatures: []string{"maskani_maintenance"}}}, false},
		{"no features at all", &mw.Access{Claims: &authclient.Claims{}}, false},
		{"demo tenant", &mw.Access{Claims: &authclient.Claims{IsDemo: true}}, true},
		{"platform owner or S2S", &mw.Access{Bypass: true}, true},
		{"no access context", nil, false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/erp/employees", nil)
		if c.a != nil {
			r = r.WithContext(mw.WithAccess(r.Context(), c.a))
		}
		w := httptest.NewRecorder()
		if got := requireERP(w, r); got != c.allow {
			t.Errorf("%s: allowed %v, want %v", c.name, got, c.allow)
		}
		if !c.allow && (w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"feature_not_available"`) ||
			!strings.Contains(w.Body.String(), `"upgrade":true`)) {
			t.Errorf("%s: %d %s", c.name, w.Code, w.Body.String())
		}
	}
}
