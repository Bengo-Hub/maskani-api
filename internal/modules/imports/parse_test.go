package imports

import (
	"strings"
	"testing"

	"github.com/bengobox/maskani-api/internal/ent"
)

func TestParseReadsColumnsInAnyOrder(t *testing.T) {
	csv := "\xef\xbb\xbfOwner_Phone,unit_code,bedrooms,extra\n0712345678,b07,3,x\n\n ,,,\n0700000000,B08,two,\n"
	rows, errs, err := Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows (blank lines skipped), got %d", len(rows))
	}
	if rows[0].UnitCode != "B07" || rows[0].OwnerPhone != "0712345678" || rows[0].Bedrooms == nil || *rows[0].Bedrooms != 3 {
		t.Fatalf("row 0 parsed wrong: %+v", rows[0])
	}
	if len(errs) != 1 || errs[0].Field != "bedrooms" || errs[0].Line != 5 {
		t.Fatalf("want one bedrooms error on line 5, got %+v", errs)
	}
}

func TestParseNeedsUnitCode(t *testing.T) {
	if _, _, err := Parse(strings.NewReader("block,owner_name\nA,Jane\n")); err == nil {
		t.Fatal("a header without unit_code must be rejected")
	}
}

func TestRedactDropsRows(t *testing.T) {
	sum := map[string]any{"rows": []Row{{UnitCode: "B07", OwnerPhone: "0712345678"}}, "counts": map[string]int{"units_create": 1}}
	out := redact(&ent.ImportJob{Summary: sum}).Summary
	if _, ok := out["rows"]; ok {
		t.Fatal("rows must not leave the service")
	}
	if _, ok := sum["rows"]; !ok {
		t.Fatal("redact must not mutate the stored map")
	}
}
