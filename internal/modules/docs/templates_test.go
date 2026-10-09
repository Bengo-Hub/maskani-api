package docs

import (
	"strings"
	"testing"
)

func TestTemplateMergeAndFields(t *testing.T) {
	body := "Unit {{unit_code}} owes {{ balance }}. Signed {{ nobody }}."
	if got := unknownFields(body, accountFields); len(got) != 1 || got[0] != "nobody" {
		t.Fatalf("unknown fields: %v", got)
	}
	got := merge(body, map[string]string{"unit_code": "A01", "balance": "KES 0"})
	if got != "Unit A01 owes KES 0. Signed __________." {
		t.Fatalf("merge: %q", got)
	}
	// Every starter template uses only its kind's fields.
	for _, k := range DocKinds {
		if bad := unknownFields(starterBodies[k.Code], k.Fields); len(bad) > 0 {
			t.Errorf("%s starter uses unknown fields %v", k.Code, bad)
		}
	}
}

func TestVerificationCode(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		c, err := verificationCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(c) != codeLen || strings.Trim(c, codeAlphabet) != "" {
			t.Fatalf("bad code %q", c)
		}
		if seen[c] {
			t.Fatalf("repeated code %q", c)
		}
		seen[c] = true
	}
}
