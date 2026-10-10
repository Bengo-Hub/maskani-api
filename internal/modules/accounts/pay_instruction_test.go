package accounts

import (
	"testing"

	"github.com/bengobox/maskani-api/internal/ent"
)

func TestPayInstruction(t *testing.T) {
	f := &ent.Fund{PaybillShortcode: "222111"}
	if p := PayInstructionFor(f, "TAN7"); p.Account != "TAN7" || p.Reference != "" || p.BankMatched {
		t.Fatalf("default: %+v", p)
	}
	f.Metadata = map[string]any{"paybill_account_format": "2362010#{ref}"}
	if p := PayInstructionFor(f, "TAN7"); p.Account != "2362010#TAN7" || !p.BankMatched {
		t.Fatalf("account#ref: %+v", p)
	}
	f.Metadata = map[string]any{"paybill_account_format": "2362010"}
	if p := PayInstructionFor(f, "TAN7"); p.Account != "2362010" || p.Reference != "TAN7" || !p.BankMatched {
		t.Fatalf("bank account only: %+v", p)
	}
	if p := PayInstructionFor(&ent.Fund{}, "TAN7"); p.Paybill != "" {
		t.Fatalf("no paybill: %+v", p)
	}
}
