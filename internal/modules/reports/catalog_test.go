package reports

import "testing"

func TestCatalogFollowsModules(t *testing.T) {
	all := Catalog(nil, func(string) bool { return true })
	if len(all) != 5 {
		t.Fatalf("every module: %d", len(all))
	}
	only := Catalog(map[string]bool{"billing": true}, func(string) bool { return true })
	if len(only) != 1 || only[0].Module != "billing" {
		t.Fatalf("billing only: %+v", only)
	}
	for _, r := range only[0].Reports {
		if r.ExportPath == "" || len(r.Formats) == 0 || r.Module != "billing" {
			t.Fatalf("report %s missing export path, formats or module", r.ID)
		}
	}
	// Every estate report in the catalog is one the API can build, and the other way round.
	ids := map[string]bool{}
	for _, m := range all {
		for _, r := range m.Reports {
			if r.Source == "estate" {
				ids[r.ID] = true
			}
		}
	}
	for _, k := range EstateReportKinds {
		if !ids[k.Kind] {
			t.Fatalf("estate report %s is not in the catalog", k.Kind)
		}
		delete(ids, k.Kind)
	}
	if len(ids) != 0 {
		t.Fatalf("catalog lists estate reports the API does not build: %v", ids)
	}
}
