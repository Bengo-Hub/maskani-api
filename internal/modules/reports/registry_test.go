package reports

import "testing"

func TestRegistryAndCatalog(t *testing.T) {
	all := Catalog(nil, func(string) bool { return true })
	if len(all) != 6 {
		t.Fatalf("every module: %d", len(all))
	}
	// Sales reports name buyers and balances: hidden without sales.view.
	for _, m := range Catalog(nil, func(p string) bool { return p != "maskani.sales.view" }) {
		if m.Module == "sales" {
			t.Fatal("sales reports need sales.view")
		}
	}
	only := Catalog(map[string]bool{"billing": true}, func(string) bool { return true })
	if len(only) != 1 || only[0].Module != "billing" || only[0].DisplayName == "" {
		t.Fatalf("billing only: %+v", only)
	}
	for _, d := range registry {
		got, ok := Lookup(d.ID)
		if !ok || got.ExportPath != "/reports/"+d.ID+"/export" || len(got.Formats) == 0 || got.Module == "" || got.Name == "" {
			t.Fatalf("report %s is incomplete: %+v", d.ID, got)
		}
		if got.Source == SourceTable && (len(got.Columns) == 0 || (got.sql == "" && got.build == nil)) {
			t.Fatalf("table report %s has no columns or no way to build it", d.ID)
		}
	}
}
