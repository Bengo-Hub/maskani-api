package settings

import "testing"

func TestPropertyModuleRule(t *testing.T) {
	tenant := map[string]bool{ModProperties: true, ModBilling: true, ModUtilities: true, ModSales: true, ModGate: true, ModCommunication: true}

	// A sales-only development: its preset drops gate and water even though the tenant has them.
	got := propertyModuleRule{useCase: "developer_sales"}.effective(tenant)
	if got[ModGate] || got[ModUtilities] || !got[ModSales] || !got[ModBilling] {
		t.Fatalf("developer_sales property: %v", got)
	}

	// An override adds back a tenant module; its dependency comes with it.
	got = propertyModuleRule{useCase: "developer_sales", overrides: map[string]bool{ModUtilities: true}}.effective(tenant)
	if !got[ModUtilities] || !got[ModBilling] {
		t.Fatalf("override on: %v", got)
	}

	// The tenant caps everything: a preset module the tenant has off stays off.
	got = propertyModuleRule{useCase: "estate_developer"}.effective(tenant)
	if got[ModMaintenance] {
		t.Fatalf("module off at tenant leaked in: %v", got)
	}

	// An override can switch a preset module off; an unknown use case means the tenant's set.
	got = propertyModuleRule{useCase: "estate_developer", overrides: map[string]bool{ModGate: false}}.effective(tenant)
	if got[ModGate] {
		t.Fatalf("override off: %v", got)
	}
	if got = (propertyModuleRule{useCase: ""}).effective(tenant); len(got) != len(tenant) {
		t.Fatalf("no use case: %v", got)
	}
}
