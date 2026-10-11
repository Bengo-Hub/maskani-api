package reports

import (
	"testing"
	"time"
)

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

func TestPgTimeZone(t *testing.T) {
	nairobi, err := time.LoadLocation("Africa/Nairobi")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		loc  *time.Location
		want string
	}{
		{nairobi, "Africa/Nairobi"},
		{time.UTC, "UTC"},
		{time.FixedZone("EAT", 3*3600), "<+0300>-03:00"},
		{time.FixedZone("X", -(5*3600 + 30*60)), "<-0530>+05:30"},
	}
	for _, c := range cases {
		if got := pgTimeZone(c.loc, now); got != c.want {
			t.Errorf("pgTimeZone(%s) = %q, want %q", c.loc, got, c.want)
		}
	}
}
