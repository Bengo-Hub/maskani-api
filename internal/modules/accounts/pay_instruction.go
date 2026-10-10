package accounts

import (
	"strings"

	"github.com/bengobox/maskani-api/internal/ent"
)

// PayInstruction says how an owner pays an account at the fund's paybill. Most estates use the
// account reference as the paybill account number (one paybill, every unit its own reference).
// A bank paybill instead needs the estate's bank account number there: the fund's metadata
// "paybill_account_format" then holds it, either with {ref} where the bank accepts a reference
// ("2362010#{ref}") or alone ("2362010"), in which case the unit reference goes with the payment
// as its reference and the money is matched from the bank side.
type PayInstruction struct {
	Paybill string `json:"paybill,omitempty"`
	// Account is what to type as the account number.
	Account string `json:"pay_account,omitempty"`
	// Reference is the unit reference to quote when Account is the bank account alone.
	Reference string `json:"pay_reference,omitempty"`
	// BankMatched is true when the paybill's confirmations do not reach us, so payments are
	// recorded from the bank side (statement import or a verified manual payment).
	BankMatched bool `json:"bank_matched,omitempty"`
}

// PayInstructionFor works out the instruction for one account reference.
func PayInstructionFor(f *ent.Fund, accountRef string) PayInstruction {
	if f == nil || f.PaybillShortcode == "" {
		return PayInstruction{}
	}
	format, _ := f.Metadata["paybill_account_format"].(string)
	format = strings.TrimSpace(format)
	if format == "" || format == "{ref}" {
		return PayInstruction{Paybill: f.PaybillShortcode, Account: accountRef}
	}
	if strings.Contains(format, "{ref}") {
		return PayInstruction{Paybill: f.PaybillShortcode, Account: strings.ReplaceAll(format, "{ref}", accountRef), BankMatched: true}
	}
	return PayInstruction{Paybill: f.PaybillShortcode, Account: format, Reference: accountRef, BankMatched: true}
}

// Payload adds the instruction to an event payload (paybill, pay_account, pay_reference).
func (p PayInstruction) Payload(m map[string]any) {
	m["paybill"], m["pay_account"], m["pay_reference"] = p.Paybill, p.Account, p.Reference
}
