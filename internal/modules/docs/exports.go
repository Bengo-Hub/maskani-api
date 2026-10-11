package docs

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Bengo-Hub/reports"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	analytics "github.com/bengobox/maskani-api/internal/modules/reports"
)

// ExportParams are the filters a report export may take; each report reads the ones it uses.
type ExportParams struct {
	Range      analytics.ReportRange
	Dashboard  analytics.DashboardFilter
	Arrears    analytics.ArrearsFilter
	MaskPhones bool
}

// Export renders any report in the registry through this one branded engine: table reports from
// their table, arrears and performance through their own layouts. Every download in Maskani, from
// the Reports page, Collections or the dashboard, comes through here.
func (s *Service) Export(ctx context.Context, tenantID uuid.UUID, slug string, sc analytics.Scope, id string, p ExportParams, format reports.Format) (*File, error) {
	def, ok := analytics.Lookup(id)
	if !ok {
		return nil, httpx.Invalid("unknown report")
	}
	switch {
	case def.ID == "arrears":
		return s.Arrears(ctx, tenantID, slug, sc, p.Arrears, p.MaskPhones, format)
	case def.ID == "performance":
		return s.Performance(ctx, tenantID, slug, sc, p.Dashboard, format)
	case def.Source == analytics.SourceTable:
		return s.tableReport(ctx, tenantID, slug, sc, id, p.Range, format)
	}
	return nil, httpx.Invalid("this report has no download")
}

// tableReport renders a table report: a chart of its main figure when it runs month by month or
// by category, then the table with its total row. It is the table the screen shows, so the file and
// the screen never disagree.
func (s *Service) tableReport(ctx context.Context, tenantID uuid.UUID, slug string, sc analytics.Scope, kind string, rg analytics.ReportRange, format reports.Format) (*File, error) {
	t, err := s.analytics.TableReport(ctx, sc, kind, rg)
	if err != nil {
		return nil, err
	}
	r := s.newReport(ctx, tenantID, slug, sc, t.Title, t.Subtitle)
	r.Meta = append(r.Meta, [2]string{"Months", monthLabel(t.From) + " to " + monthLabel(t.To)})
	cur := r.Currency

	cols := make([]reports.Column, len(t.Columns))
	for i, c := range t.Columns {
		cols[i] = reports.Column{Header: c.Label, Weight: 1.3, Align: "R", Money: c.Kind == "money"}
		if c.Kind == "text" {
			cols[i] = reports.Column{Header: c.Label, Weight: 2.2}
		}
	}
	cell := func(c analytics.Column, v any) string {
		if v == nil {
			return "-"
		}
		raw := fmt.Sprint(v)
		switch c.Kind {
		case "money":
			d, _ := decimal.NewFromString(raw)
			return moneyText(cur, d)
		case "pct":
			return raw + "%"
		case "hours":
			return raw + " h"
		case "text":
			if c.Key == "period" {
				return monthLabel(raw)
			}
			if c.Key == "category" {
				return titleCase(raw)
			}
		}
		return raw
	}
	rows := make([][]reports.Cell, len(t.Rows))
	for i, row := range t.Rows {
		rows[i] = make([]reports.Cell, len(t.Columns))
		for j, c := range t.Columns {
			rows[i][j] = reports.Text(cell(c, row[c.Key]))
		}
	}
	table := reports.Section{Kind: reports.SectionTable, Title: t.Title, Note: t.Note, Columns: cols, Rows: rows}
	if len(t.Totals) > 0 {
		table.Total = make([]reports.Cell, len(t.Columns))
		for j, c := range t.Columns {
			switch {
			case j == 0:
				table.Total[j] = reports.BoldText("Total")
			case t.Totals[c.Key] != nil:
				table.Total[j] = reports.BoldText(cell(c, t.Totals[c.Key]))
			default:
				table.Total[j] = reports.Text("")
			}
		}
	}

	// The chart shows the first money or count column after the label, one bar per row.
	var chart *reports.Section
	if len(t.Rows) > 0 && len(t.Columns) > 1 {
		val := t.Columns[1]
		for _, c := range t.Columns[1:] {
			if c.Kind == "money" {
				val = c
				break
			}
		}
		bars := make([]reports.Bar, len(t.Rows))
		for i, row := range t.Rows {
			f, _ := strconv.ParseFloat(fmt.Sprint(row[val.Key]), 64)
			label := fmt.Sprint(row[t.Columns[0].Key])
			if t.Columns[0].Key == "period" {
				label = shortMonth(label)
			}
			bars[i] = reports.Bar{Label: label, Value: f}
		}
		unit := ""
		if val.Kind == "money" {
			unit = cur
		}
		chart = &reports.Section{Kind: reports.SectionChart, Title: val.Label, Bars: bars, ValueUnit: unit, Horizontal: t.Columns[0].Key != "period"}
	}
	if chart != nil {
		r.Sections = append(r.Sections, *chart)
	}
	r.Sections = append(r.Sections, table)
	return s.render(r, format, kind+"-"+t.From+"-"+t.To)
}
