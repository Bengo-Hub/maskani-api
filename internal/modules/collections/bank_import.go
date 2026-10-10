package collections

import (
	"context"
	"regexp"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/fund"
	"github.com/bengobox/maskani-api/internal/ent/unit"
	"github.com/bengobox/maskani-api/internal/ent/unitaccount"
	"github.com/bengobox/maskani-api/internal/http/httpx"
)

// BankLine is one credit from a bank statement (the screen parses the bank's CSV).
type BankLine struct {
	Date        string          `json:"date"` // YYYY-MM-DD
	Amount      decimal.Decimal `json:"amount"`
	Reference   string          `json:"reference"` // the bank's transaction reference
	Description string          `json:"description"`
	Payer       string          `json:"payer"`
}

// BankLineResult says what happened to one line.
type BankLineResult struct {
	Line            BankLine   `json:"line"`
	Status          string     `json:"status"` // queued, duplicate, unmatched, invalid
	AccountID       *uuid.UUID `json:"account_id,omitempty"`
	AccountRef      string     `json:"account_ref,omitempty"`
	MatchedBy       string     `json:"matched_by,omitempty"` // reference or phone
	ManualPaymentID *uuid.UUID `json:"manual_payment_id,omitempty"`
	Error           string     `json:"error,omitempty"`
}

const maxBankLines = 2000

var phoneRe = regexp.MustCompile(`(?:\+?254|0)(7\d{8}|1\d{8})`)

// matchKey normalises an account reference the way treasury does for paybill references: upper
// case, separators dropped, leading zeros stripped from each digit run ("B-07" and "b7" give "B7").
func matchKey(ref string) string {
	var out, digits strings.Builder
	flush := func() {
		d := strings.TrimLeft(digits.String(), "0")
		if d == "" && digits.Len() > 0 {
			d = "0"
		}
		out.WriteString(d)
		digits.Reset()
	}
	for _, r := range strings.ToUpper(ref) {
		switch {
		case unicode.IsDigit(r):
			digits.WriteRune(r)
		case unicode.IsLetter(r):
			flush()
			out.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return out.String()
}

// tokens splits text into the words an account reference could be (and "acct#ref" halves).
func tokens(text string) []string {
	return strings.FieldsFunc(strings.ToUpper(text), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '/')
	})
}

// ImportBankLines matches bank statement credits to the fund's accounts, by a unit reference in the
// description or reference, else by the owner's phone, and queues each match for review as a bank
// transfer. Lines already queued (same bank reference) are reported as duplicates; the rest are
// unmatched for someone to record by hand.
func (s *Service) ImportBankLines(ctx context.Context, fundCode string, propertyIDs []uuid.UUID, all bool, by Submitter, lines []BankLine) ([]BankLineResult, error) {
	by.Quiet = true
	if len(lines) == 0 || len(lines) > maxBankLines {
		return nil, httpx.Invalid("send between 1 and 2000 statement lines")
	}
	f, err := s.client.Fund.Query().Where(fund.Code(fundCode)).Only(ctx)
	if err != nil {
		return nil, httpx.Invalid("unknown fund")
	}
	q := s.client.UnitAccount.Query().Where(unitaccount.FundID(f.ID), unitaccount.StatusEQ(unitaccount.StatusActive))
	if !all {
		q = q.Where(unitaccount.HasUnitWith(unit.PropertyIDIn(propertyIDs...)))
	}
	accs, err := q.Select(unitaccount.FieldID, unitaccount.FieldAccountRef, unitaccount.FieldCustomerPhone).All(ctx)
	if err != nil {
		return nil, err
	}
	byKey := map[string]*ent.UnitAccount{}
	byPhone := map[string]*ent.UnitAccount{}
	ambiguousPhone := map[string]bool{}
	for _, a := range accs {
		if k := matchKey(a.AccountRef); len(k) >= 2 {
			byKey[k] = a
		}
		if m := phoneRe.FindStringSubmatch(a.CustomerPhone); m != nil {
			if _, seen := byPhone[m[1]]; seen {
				ambiguousPhone[m[1]] = true // one phone, several accounts: never match by it
			}
			byPhone[m[1]] = a
		}
	}
	out := make([]BankLineResult, 0, len(lines))
	for _, l := range lines {
		r := BankLineResult{Line: l}
		if !l.Amount.IsPositive() || strings.TrimSpace(l.Reference) == "" {
			r.Status, r.Error = "invalid", "a credit needs an amount and the bank reference"
			out = append(out, r)
			continue
		}
		text := l.Reference + " " + l.Description
		var acc *ent.UnitAccount
		for _, t := range tokens(text) {
			// "2362010#TAN7" arrives as one token without the '#': try the whole and each half.
			for _, part := range append([]string{t}, strings.Split(t, "#")...) {
				if a := byKey[matchKey(part)]; a != nil {
					acc, r.MatchedBy = a, "reference"
					break
				}
			}
			if acc != nil {
				break
			}
		}
		if acc == nil {
			for _, m := range phoneRe.FindAllStringSubmatch(text+" "+l.Payer, -1) {
				if a := byPhone[m[1]]; a != nil && !ambiguousPhone[m[1]] {
					acc, r.MatchedBy = a, "phone"
					break
				}
			}
		}
		if acc == nil {
			r.Status = "unmatched"
			out = append(out, r)
			continue
		}
		id := acc.ID
		r.AccountID, r.AccountRef = &id, acc.AccountRef
		mp, err := s.SubmitManual(ctx, acc.ID, by, ManualInput{Amount: l.Amount, Method: "bank_transfer",
			Reference: l.Reference, PaidOn: l.Date, PayerName: l.Payer, Note: strings.TrimSpace("Bank statement: " + l.Description)})
		switch {
		case err == nil:
			r.Status, r.ManualPaymentID = "queued", &mp.ID
		case isConflict(err):
			r.Status = "duplicate"
		default:
			r.Status, r.Error = "invalid", err.Error()
		}
		out = append(out, r)
	}
	return out, nil
}

func isConflict(err error) bool {
	_, ok := err.(*httpx.ConflictError)
	return ok
}
