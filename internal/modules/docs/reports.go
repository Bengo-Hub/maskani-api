package docs

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Bengo-Hub/reports"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/bengobox/maskani-api/internal/ent/property"
	analytics "github.com/bengobox/maskani-api/internal/modules/reports"
	"github.com/bengobox/maskani-api/internal/shared/secure"
)

// Arrears renders every owing account in scope, largest first, with the ageing breakdown. Phones
// are masked unless the caller follows up arrears (same rule as the screen).
func (s *Service) Arrears(ctx context.Context, tenantID uuid.UUID, slug string, sc analytics.Scope, f analytics.ArrearsFilter, maskPhones bool, format reports.Format) (*File, error) {
	rows, cut, err := s.analytics.ArrearsAll(ctx, sc, f)
	if err != nil {
		return nil, err
	}
	ageing, err := s.analytics.Ageing(ctx, sc)
	if err != nil {
		return nil, err
	}
	r := s.newReport(ctx, tenantID, slug, sc, "Arrears", "Owing balances")
	cur := r.Currency

	total := decimal.Zero
	table := make([][]reports.Cell, len(rows))
	for i, a := range rows {
		total = total.Add(a.Balance)
		phone := a.Phone
		if maskPhones {
			phone = secure.MaskPhone(phone)
		}
		last := "No payment yet"
		if a.LastPaid != nil {
			last = reports.Date(a.LastPaid.In(s.loc))
		}
		table[i] = []reports.Cell{reports.Text(a.AccountRef), reports.Text(a.Customer), reports.Text(phone),
			reports.Text(moneyText(cur, a.Balance)), reports.Text(last)}
	}
	over60, bars := decimal.Zero, make([]reports.Bar, len(ageing))
	for i, b := range ageing {
		if b.Bucket == "61-90" || b.Bucket == "90+" {
			over60 = over60.Add(b.Amount)
		}
		bars[i] = reports.Bar{Label: b.Bucket + " days", Value: b.Amount.InexactFloat64()}
	}
	count := strconv.Itoa(len(rows))
	if cut {
		count += "+"
	}
	r.Cards = []reports.Card{
		{Label: "Accounts owing", Value: count},
		{Label: "Total owing", Value: moneyText(cur, total)},
		{Label: "Over 60 days", Value: moneyText(cur, over60)},
	}
	notes := []string{}
	if f.Q != "" {
		notes = append(notes, fmt.Sprintf("matching %q", f.Q))
	}
	if f.Min.IsPositive() {
		notes = append(notes, "owing at least "+moneyText(cur, f.Min))
	}
	if cut {
		notes = append(notes, fmt.Sprintf("the first %d by balance", analytics.ExportRowLimit))
	}
	note := ""
	if len(notes) > 0 {
		note = "Accounts " + strings.Join(notes, ", ") + "."
	}
	r.Sections = []reports.Section{
		{Kind: reports.SectionChart, Title: "Owing by age", Note: "Days since the oldest unpaid bill fell due", Bars: bars, ValueUnit: cur},
		{
			Kind: reports.SectionTable, Title: "Accounts owing", Note: note,
			Columns: []reports.Column{{Header: "Account", Weight: 1.4}, {Header: "Billed to", Weight: 2.6}, {Header: "Phone", Weight: 1.6},
				{Header: "Balance", Weight: 1.6, Money: true}, {Header: "Last paid", Weight: 1.4}},
			Rows:  table,
			Total: []reports.Cell{reports.BoldText("Total"), {}, {}, reports.BoldText(moneyText(cur, total)), {}},
		},
	}
	return s.render(r, format, "arrears")
}

// Performance renders the business view behind the staff dashboard for a month: headline figures
// with comparisons, the 12-month trend, the cash-in forecast and its method, revenue by charge,
// blocks, maintenance and the sales pace.
func (s *Service) Performance(ctx context.Context, tenantID uuid.UUID, slug string, sc analytics.Scope, period string, format reports.Format) (*File, error) {
	v, err := s.analytics.Insights(ctx, sc, period)
	if err != nil {
		return nil, err
	}
	r := s.newReport(ctx, tenantID, slug, sc, "Performance", "Month of "+monthLabel(v.Period))
	performanceReport(r, v)
	return s.render(r, format, "performance-"+v.Period)
}

// performanceReport fills a branded report with the insights.
func performanceReport(r *reports.Report, v *analytics.Insights) {
	cur := r.Currency
	k := v.KPIs

	r.Cards = []reports.Card{
		{Label: "Collection rate", Value: pctText(&k.CollectionRate.Value), Sub: change(k.CollectionRate.Value, k.CollectionRate.LastMonth, "points on last month", true)},
		{Label: "Billed", Value: moneyText(cur, k.Billed.Value), Sub: change(k.Billed.Value, k.Billed.LastMonth, "on last month", false)},
		{Label: "Collected", Value: moneyText(cur, k.Collected.Value), Sub: change(k.Collected.Value, k.Collected.LastMonth, "on last month", false)},
		{Label: "Owing now", Value: moneyText(cur, k.Outstanding), Sub: daysText(k.DaysSalesOutstanding)},
	}

	trend := make([][]reports.Cell, len(v.Months))
	bars := make([]reports.Bar, len(v.Months))
	for i, m := range v.Months {
		trend[i] = []reports.Cell{reports.Text(monthLabel(m.Period)), reports.Text(moneyText(cur, m.Billed)),
			reports.Text(moneyText(cur, m.Collected)), reports.Text(pctText(m.CollectionRate))}
		bars[i] = reports.Bar{Label: shortMonth(m.Period), Value: m.Collected.InexactFloat64()}
	}
	forecast := make([][]reports.Cell, len(v.Forecast))
	for i, f := range v.Forecast {
		forecast[i] = []reports.Cell{reports.Text(monthLabel(f.Period)), reports.Text(moneyText(cur, f.Instalments)),
			reports.Text(moneyText(cur, f.Recurring)), reports.BoldText(moneyText(cur, f.Total))}
	}
	mix := make([]reports.Bar, len(v.RevenueMix))
	for i, m := range v.RevenueMix {
		mix[i] = reports.Bar{Label: titleCase(m.ChargeCode), Value: m.Amount.InexactFloat64()}
	}
	blocks := make([][]reports.Cell, len(v.Blocks))
	for i, b := range v.Blocks {
		blocks[i] = []reports.Cell{reports.Text(b.Name), reports.Text(strconv.Itoa(b.Units)),
			reports.Text(moneyText(cur, b.Billed)), reports.Text(moneyText(cur, b.Owing))}
	}
	work := make([][]reports.Cell, len(v.WorkByType))
	for i, w := range v.WorkByType {
		work[i] = []reports.Cell{reports.Text(titleCase(w.Category)), reports.Text(strconv.Itoa(w.Opened)),
			reports.Text(strconv.Itoa(w.Completed)), reports.Text(strconv.Itoa(w.Breached)), reports.Text(hoursText(w.AvgResolveHours))}
	}

	r.Sections = []reports.Section{
		{Kind: reports.SectionChart, Title: "Collected by month", Bars: bars, ValueUnit: cur},
		{Kind: reports.SectionTable, Title: "Billed and collected", Columns: []reports.Column{{Header: "Month", Weight: 2},
			{Header: "Billed", Weight: 2, Money: true}, {Header: "Collected", Weight: 2, Money: true}, {Header: "Collection rate", Weight: 1.4, Align: "R"}}, Rows: trend},
		{Kind: reports.SectionTable, Title: "Expected cash in", Note: v.ForecastBasis.Method, Columns: []reports.Column{{Header: "Month", Weight: 2},
			{Header: "Instalments", Weight: 2, Money: true}, {Header: "Recurring charges", Weight: 2, Money: true}, {Header: "Total", Weight: 2, Money: true}}, Rows: forecast},
	}
	if len(mix) > 0 {
		r.Sections = append(r.Sections, reports.Section{Kind: reports.SectionChart, Title: "Revenue by charge", Note: "Billed over the last three complete months", Horizontal: true, Bars: mix, ValueUnit: cur})
	}
	if len(blocks) > 0 {
		r.Sections = append(r.Sections, reports.Section{Kind: reports.SectionTable, Title: "Blocks", Columns: []reports.Column{{Header: "Block", Weight: 2.4},
			{Header: "Units", Weight: 1, Align: "R"}, {Header: "Billed last month", Weight: 2, Money: true}, {Header: "Owing now", Weight: 2, Money: true}}, Rows: blocks})
	}
	if len(work) > 0 {
		r.Sections = append(r.Sections, reports.Section{Kind: reports.SectionTable, Title: "Maintenance by category", Note: "Last 90 days",
			Columns: []reports.Column{{Header: "Category", Weight: 2.4}, {Header: "Opened", Weight: 1, Align: "R"}, {Header: "Completed", Weight: 1.2, Align: "R"},
				{Header: "Past due time", Weight: 1.3, Align: "R"}, {Header: "Average time to close", Weight: 1.8, Align: "R"}}, Rows: work})
	}
	sales := v.Sales
	pairs := []reports.KV{
		{Label: "Units", Value: strconv.Itoa(k.Units)},
		{Label: "Occupied", Value: fmt.Sprintf("%d (%s)", k.Occupied, pctText(k.OccupancyPct))},
		{Label: "Open work orders", Value: strconv.Itoa(k.OpenWorkOrders)},
		{Label: "Available for sale", Value: strconv.Itoa(sales.Available)},
		{Label: "Contracts signed, last 6 months", Value: strconv.Itoa(sales.SignedLast6m)},
	}
	if sales.MonthsToSellOut != nil {
		pairs = append(pairs, reports.KV{Label: "Months to sell out at this pace", Value: sales.MonthsToSellOut.StringFixed(1)})
	}
	pairs = append(pairs,
		reports.KV{Label: "Contract value", Value: moneyText(cur, sales.ContractValue)},
		reports.KV{Label: "Collected on contracts", Value: moneyText(cur, sales.ContractCollected), Bold: true, Rule: true})
	r.Sections = append(r.Sections, reports.Section{Kind: reports.SectionKeyValue, Title: "Occupancy and sales", Pairs: pairs})
}

// WaterBalance renders supplied against billed water for a property over six months.
func (s *Service) WaterBalance(ctx context.Context, tenantID uuid.UUID, slug string, propertyID uuid.UUID, period string, format reports.Format) (*File, error) {
	points, err := s.analytics.WaterBalance(ctx, propertyID, period)
	if err != nil {
		return nil, err
	}
	r := s.newReport(ctx, tenantID, slug, analytics.Scope{PropertyID: &propertyID}, "Water balance", "Supplied against billed")
	m3 := func(d decimal.Decimal) string { return reports.Quantity(d.Round(1).InexactFloat64()) + " m3" }
	rows := make([][]reports.Cell, len(points))
	bars := make([]reports.Bar, len(points))
	for i, p := range points {
		rows[i] = []reports.Cell{reports.Text(monthLabel(p.Period)), reports.Text(m3(p.Supplied)), reports.Text(m3(p.Billed)),
			reports.Text(m3(p.Common)), reports.Text(m3(p.Lost)), reports.Text(pctText(&p.LossPct)), reports.Text(strconv.Itoa(p.Estimated))}
		bars[i] = reports.Bar{Label: shortMonth(p.Period), Value: p.LossPct.InexactFloat64()}
	}
	r.Currency = ""
	r.Sections = []reports.Section{
		{Kind: reports.SectionChart, Title: "Water lost, percent of supply", Bars: bars, ValueUnit: "%"},
		{Kind: reports.SectionTable, Title: "By month", Note: "Unaccounted is supplied less billed and common use",
			Columns: []reports.Column{{Header: "Month", Weight: 2}, {Header: "Supplied", Weight: 1.4, Align: "R"}, {Header: "Billed", Weight: 1.4, Align: "R"},
				{Header: "Common", Weight: 1.3, Align: "R"}, {Header: "Unaccounted", Weight: 1.5, Align: "R"}, {Header: "Loss", Weight: 1, Align: "R"},
				{Header: "Estimated readings", Weight: 1.6, Align: "R"}},
			Rows: rows},
	}
	return s.render(r, format, "water-balance-"+period)
}

// newReport starts a branded report for a scope: the property's name as the outlet when there is
// one, else "All properties".
func (s *Service) newReport(ctx context.Context, tenantID uuid.UUID, slug string, sc analytics.Scope, title, subtitle string) *reports.Report {
	r := &reports.Report{Title: title, Subtitle: subtitle, GeneratedAt: time.Now().In(s.loc), Currency: "KES"}
	s.brand.Apply(ctx, tenantID, slug, r)
	switch {
	case sc.PropertyID != nil:
		if p, err := s.client.Property.Query().Where(property.ID(*sc.PropertyID)).Select(property.FieldName).Only(ctx); err == nil {
			r.OutletName = p.Name
			r.Meta = append(r.Meta, [2]string{"Property", p.Name})
		}
	case sc.All:
		r.Meta = append(r.Meta, [2]string{"Properties", "All"})
	default:
		r.Meta = append(r.Meta, [2]string{"Properties", "Yours"})
	}
	return r
}

func (s *Service) render(r *reports.Report, format reports.Format, name string) (*File, error) {
	body, mime, err := reports.Generate(r, format)
	if err != nil {
		return nil, err
	}
	return &File{Body: body, Mime: mime, Name: fmt.Sprintf("%s-%s.%s", fileSafe(name), r.GeneratedAt.Format("2006-01-02"), format.Ext())}, nil
}

func moneyText(cur string, d decimal.Decimal) string { return reports.Money(cur, d.InexactFloat64()) }

func pctText(d *decimal.Decimal) string {
	if d == nil {
		return "-"
	}
	return d.StringFixed(1) + "%"
}

func hoursText(d *decimal.Decimal) string {
	if d == nil {
		return "-"
	}
	h := d.InexactFloat64()
	if h >= 48 {
		return fmt.Sprintf("%.1f days", h/24)
	}
	return fmt.Sprintf("%.0f hours", h)
}

func daysText(d *decimal.Decimal) string {
	if d == nil {
		return ""
	}
	return d.StringFixed(0) + " days of billing"
}

// change words a month-on-month move: "+4.2% on last month". points compares percentages in
// points rather than as a ratio.
func change(now decimal.Decimal, before *decimal.Decimal, words string, points bool) string {
	if before == nil {
		return ""
	}
	var diff decimal.Decimal
	if points {
		diff = now.Sub(*before)
	} else {
		if before.IsZero() {
			return ""
		}
		diff = now.Sub(*before).Div(*before).Mul(decimal.NewFromInt(100))
	}
	sign := ""
	if diff.IsPositive() {
		sign = "+"
	}
	unit := "%"
	if points {
		unit = ""
	}
	return sign + diff.StringFixed(1) + unit + " " + words
}

// monthLabel turns "2026-09" into "September 2026"; anything else passes through.
func monthLabel(period string) string {
	if t, err := time.Parse("2006-01", period); err == nil {
		return t.Format("January 2006")
	}
	return period
}

func shortMonth(period string) string {
	if t, err := time.Parse("2006-01", period); err == nil {
		return t.Format("Jan 06")
	}
	return period
}

// titleCase turns a code ("service_charge") into words ("Service charge").
func titleCase(code string) string {
	s := strings.ReplaceAll(code, "_", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
