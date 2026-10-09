package docs

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Bengo-Hub/reports"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/property"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/accounts"
	"github.com/bengobox/maskani-api/internal/modules/collections"
	analytics "github.com/bengobox/maskani-api/internal/modules/reports"
)

// Service builds Maskani's documents.
type Service struct {
	client      *ent.Client
	brand       *Brander
	collections *collections.Service
	analytics   *analytics.Service
	loc         *time.Location
	store       Storage
	num         Numberer
}

// NewService creates the documents service.
func NewService(client *ent.Client, brand *Brander, col *collections.Service, an *analytics.Service, loc *time.Location) *Service {
	return &Service{client: client, brand: brand, collections: col, analytics: an, loc: loc}
}

// File is a rendered document ready to download.
type File struct {
	Body []byte
	Mime string
	Name string
}

// Statement renders an account statement: every bill and payment treasury returns (up to its
// maximum of 500 of each), oldest first, with the balance after each. The caller has authorised
// the account.
func (s *Service) Statement(ctx context.Context, tenantID uuid.UUID, slug string, accountID uuid.UUID, format reports.Format) (*File, error) {
	st, err := s.collections.FullStatement(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if st.Ledger == nil {
		return nil, httpx.Unavailable("the accounts service is not answering; try again in a minute")
	}
	now := time.Now().In(s.loc)
	r := &reports.Report{GeneratedAt: now, Currency: "KES"}
	s.brand.Apply(ctx, tenantID, slug, r)
	if u := st.Account.Edges.Unit; u != nil {
		if p, err := s.client.Property.Query().Where(property.ID(u.PropertyID)).Select(property.FieldName).Only(ctx); err == nil {
			r.OutletName = p.Name
		}
	}
	statementReport(r, st, s.loc)
	return s.render(r, format, "statement-"+st.Account.AccountRef)
}

// statementReport fills a branded report with the statement: account meta, summary cards and the
// entries oldest first with the balance after each.
func statementReport(r *reports.Report, st *collections.Statement, loc *time.Location) {
	acc, led := st.Account, st.Ledger
	r.Title, r.Subtitle, r.PeriodTo = "Statement", "Account "+acc.AccountRef, r.GeneratedAt
	head := [][2]string{{"Account", acc.AccountRef}}
	if u := acc.Edges.Unit; u != nil {
		head = append(head, [2]string{"Unit", u.Code})
	}
	if acc.CustomerName != "" {
		head = append(head, [2]string{"Billed to", acc.CustomerName})
	}
	if f := acc.Edges.Fund; f != nil {
		head = append(head, [2]string{"Fund", f.Name})
		if f.PaybillShortcode != "" {
			head = append(head, [2]string{"Paybill", f.PaybillShortcode + ", account " + acc.AccountRef})
		}
	}
	r.Meta = append(head, r.Meta...)

	money := func(d decimal.Decimal) string { return reports.Money(r.Currency, d.InexactFloat64()) }
	due := led.Balance.Sub(led.Credit)
	r.Cards = []reports.Card{
		{Label: "Balance due", Value: money(decimal.Max(due, decimal.Zero))},
		{Label: "Billed", Value: money(led.TotalBilled)},
		{Label: "Paid", Value: money(led.TotalPaid)},
		{Label: "In credit", Value: money(decimal.Max(due.Neg(), decimal.Zero))},
	}

	rows := make([][]reports.Cell, 0, len(st.Entries))
	for i := len(st.Entries) - 1; i >= 0; i-- { // oldest first reads like a printed statement
		e := st.Entries[i]
		billed, paid := "", ""
		if e.Debit.IsPositive() {
			billed = money(e.Debit)
		}
		if e.Credit.IsPositive() {
			paid = money(e.Credit)
		}
		rows = append(rows, []reports.Cell{
			reports.Text(reports.Date(e.Date.In(loc))), reports.Text(e.Label), reports.Text(e.Reference),
			reports.Text(billed), reports.Text(paid), reports.Text(balanceText(r.Currency, e.BalanceAfter)),
		})
	}
	if len(st.Entries) > 0 {
		r.PeriodFrom = st.Entries[len(st.Entries)-1].Date.In(loc)
	}
	note := ""
	if st.Trimmed {
		note = fmt.Sprintf("The latest %d bills and payments. Ask the estate office for anything older.", accounts.ExportLedgerLimit)
	}
	r.Sections = []reports.Section{{
		Kind: reports.SectionTable, Title: "Bills and payments", Note: note,
		Columns: []reports.Column{
			{Header: "Date", Weight: 1.3}, {Header: "Details", Weight: 3}, {Header: "Reference", Weight: 1.6},
			{Header: "Billed", Weight: 1.5, Money: true}, {Header: "Paid", Weight: 1.5, Money: true},
			{Header: "Balance", Weight: 1.6, Money: true},
		},
		Rows:  rows,
		Total: []reports.Cell{reports.BoldText("Balance due"), {}, {}, {}, {}, reports.BoldText(balanceText(r.Currency, due))},
	}}
}

// balanceText shows a credit as a negative figure, which the renderer colours and Excel sums.
func balanceText(currency string, d decimal.Decimal) string {
	return reports.Money(currency, d.InexactFloat64())
}

// fileSafe keeps letters, digits and dashes for a download file name.
func fileSafe(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			return r
		}
		return '-'
	}, s)
}
