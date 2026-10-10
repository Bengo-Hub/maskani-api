package collections

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/unitaccount"
	"github.com/bengobox/maskani-api/internal/modules/accounts"
	"github.com/bengobox/maskani-api/internal/modules/treasury"
)

// Statement is an account with treasury's ledger and its entries in date order, newest first,
// each with the balance after it.
type Statement struct {
	Account *ent.UnitAccount        `json:"account"`
	Ledger  *treasury.AccountLedger `json:"ledger"`
	Entries []StatementEntry        `json:"entries"`
	// Trimmed is true when treasury's list of bills or payments was full, so older history exists
	// beyond the entries shown (they stop where both lists are complete).
	Trimmed bool `json:"trimmed"`
	// Pay says how to pay this account at its paybill (account number, or bank account and reference).
	Pay accounts.PayInstruction `json:"pay"`
}

// StatementEntry is one bill or payment.
type StatementEntry struct {
	Kind         string          `json:"kind"` // bill, credit or payment
	Date         time.Time       `json:"date"`
	Label        string          `json:"label"`
	Reference    string          `json:"reference,omitempty"`
	Debit        decimal.Decimal `json:"debit"`
	Credit       decimal.Decimal `json:"credit"`
	Status       string          `json:"status,omitempty"`
	BalanceAfter decimal.Decimal `json:"balance_after"`
}

// Statement returns the account's latest ledger from treasury (refreshing the cached balance) with
// its entries.
func (s *Service) Statement(ctx context.Context, accountID uuid.UUID) (*Statement, error) {
	return s.statement(ctx, accountID, accounts.LedgerLimit)
}

// FullStatement is Statement with as much history as treasury returns, for downloads.
func (s *Service) FullStatement(ctx context.Context, accountID uuid.UUID) (*Statement, error) {
	return s.statement(ctx, accountID, accounts.ExportLedgerLimit)
}

func (s *Service) statement(ctx context.Context, accountID uuid.UUID, limit int) (*Statement, error) {
	acc, err := s.client.UnitAccount.Query().Where(unitaccount.ID(accountID)).WithFund().WithUnit().Only(ctx)
	if err != nil {
		return nil, err
	}
	led, err := s.accounts.RefreshLedger(ctx, acc, limit)
	if err != nil {
		s.log.Warn("ledger unavailable", zap.String("account", acc.AccountRef), zap.Error(err))
		return &Statement{Account: acc, Entries: []StatementEntry{}, Pay: accounts.PayInstructionFor(acc.Edges.Fund, acc.AccountRef)}, nil
	}
	entries, trimmed := Entries(led, limit)
	return &Statement{Account: acc, Ledger: led, Entries: entries, Trimmed: trimmed, Pay: accounts.PayInstructionFor(acc.Edges.Fund, acc.AccountRef)}, nil
}

// Entries merges a ledger's bills, credits and payments newest first and works the balance after each one
// backwards from today's position (balance due less held credit). When a list came back full
// (limit entries), anything older than its oldest entry is dropped: bills or payments before that
// point are missing, so their balances would be wrong.
func Entries(led *treasury.AccountLedger, limit int) ([]StatementEntry, bool) {
	out := make([]StatementEntry, 0, len(led.Invoices)+len(led.Payments))
	for _, inv := range led.Invoices {
		label := inv.Description
		if label == "" {
			label = "Bill " + inv.InvoiceNumber
		}
		out = append(out, StatementEntry{Kind: "bill", Date: inv.InvoiceDate, Label: label, Reference: inv.InvoiceNumber,
			Debit: inv.TotalAmount, Status: inv.PaymentStatus})
		// Credit notes and waivers on the bill, shown just after it so every balance stays right.
		if inv.AmountCredited.IsPositive() {
			out = append(out, StatementEntry{Kind: "credit", Date: inv.InvoiceDate.Add(time.Second), Label: "Credit on " + inv.InvoiceNumber,
				Reference: inv.InvoiceNumber, Credit: inv.AmountCredited})
		}
	}
	for _, p := range led.Payments {
		label := "Payment"
		if p.Method != "" {
			label += ", " + strings.ReplaceAll(p.Method, "_", " ")
		}
		out = append(out, StatementEntry{Kind: "payment", Date: p.PaidAt, Label: label, Reference: p.Reference, Credit: p.Amount})
	}
	// Newest first; on the same instant a payment sorts above the bill it settles.
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Date.Equal(out[j].Date) {
			return out[i].Date.After(out[j].Date)
		}
		return out[i].Kind == "payment" && out[j].Kind == "bill"
	})

	var cutoff time.Time
	trimmed := false
	if len(led.Invoices) >= limit && len(led.Invoices) > 0 {
		cutoff, trimmed = oldest(led.Invoices, func(i treasury.LedgerInvoice) time.Time { return i.InvoiceDate }), true
	}
	if len(led.Payments) >= limit && len(led.Payments) > 0 {
		if c := oldest(led.Payments, func(p treasury.LedgerPayment) time.Time { return p.PaidAt }); !trimmed || c.After(cutoff) {
			cutoff = c
		}
		trimmed = true
	}

	after := led.Balance.Sub(led.Credit)
	kept := out[:0]
	for _, e := range out {
		if trimmed && e.Date.Before(cutoff) {
			break // sorted newest first: everything from here is older
		}
		e.BalanceAfter = after
		after = after.Sub(e.Debit).Add(e.Credit)
		kept = append(kept, e)
	}
	return kept, trimmed
}

func oldest[T any](rows []T, at func(T) time.Time) time.Time {
	first := at(rows[0])
	for _, r := range rows[1:] {
		if t := at(r); t.Before(first) {
			first = t
		}
	}
	return first
}
