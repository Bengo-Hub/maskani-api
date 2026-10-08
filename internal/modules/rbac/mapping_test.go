package rbac

import "testing"

// A mixed tenant (codevertex-demo hosts every vertical) must not pull other products' staff into
// Maskani through generic role names; a property tenant maps them as before.
func TestMapSSORoleGenericNamesOnlyForPropertyTenants(t *testing.T) {
	cases := []struct {
		role     string
		mixed    string
		property string
	}{
		{"admin", RoleTenantAdmin, RoleTenantAdmin},
		{"owner", RoleTenantAdmin, RoleTenantAdmin},
		{"property_manager", RolePropertyManager, RolePropertyManager},
		{"estate_accountant", RoleFinanceOfficer, RoleFinanceOfficer},
		{"property_sales", RoleSalesOfficer, RoleSalesOfficer},
		{"caretaker", RoleCaretaker, RoleCaretaker},
		{"estate_security", RoleSecurityManager, RoleSecurityManager},
		{"maskani_owner", RoleOwner, RoleOwner},
		{"manager", "", RolePropertyManager},
		{"cashier", "", RoleFinanceOfficer},
		{"member", "", RoleViewer},
		{"security", "", RoleSecurityManager},
		{"customer", "", ""},
		{"waiter", "", ""},
		{"doctor", "", ""},
	}
	for _, c := range cases {
		if got := MapSSORole(c.role, false); got != c.mixed {
			t.Errorf("mixed tenant %q: got %q, want %q", c.role, got, c.mixed)
		}
		if got := MapSSORole(c.role, true); got != c.property {
			t.Errorf("property tenant %q: got %q, want %q", c.role, got, c.property)
		}
	}
}

func TestIsPropertyUseCase(t *testing.T) {
	for _, v := range []string{"property", "estate", "real_estate", "maskani"} {
		if !IsPropertyUseCase(v) {
			t.Errorf("%q should be a property use case", v)
		}
	}
	for _, v := range []string{"", "hospitality", "retail", "hospital", "logistics"} {
		if IsPropertyUseCase(v) {
			t.Errorf("%q should not be a property use case", v)
		}
	}
}
