package reports

import (
	"context"
	"time"
)

// The report registry is the one place every Maskani report is defined: its name, module, filters
// and, for table reports, its columns and SQL. The catalog the console reads, the table builder and
// the docs engine's exports all work from it, the same pattern as TruLoad's report catalog, so a
// report is added in one place and appears everywhere.

// Report sources: how the screen shows a report.
const (
	SourceTable   = "table"   // a table from GET /reports/{id}/table
	SourceArrears = "arrears" // the keyset arrears list on Collections
	SourceFile    = "file"    // download only
)

// Definition is one report.
type Definition struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Module      string   `json:"module"`
	Source      string   `json:"source"`
	Filters     []string `json:"filters"` // range, property, period, min_owing
	Formats     []string `json:"formats"`
	// ExportPath is the download path under the tenant base; every report exports the same way.
	ExportPath    string `json:"export_path"`
	Permission    string `json:"permission,omitempty"`
	NeedsProperty bool   `json:"needs_property,omitempty"`

	// Table reports only.
	Subtitle string   `json:"-"`
	Columns  []Column `json:"-"`
	Note     string   `json:"-"`
	sql      string
	build    func(ctx context.Context, s *Service, sc Scope, t *Table, to time.Time) (*Table, error)
}

// CatalogModule is one module's reports in the catalog.
type CatalogModule struct {
	Module      string       `json:"module"`
	DisplayName string       `json:"display_name"`
	Reports     []Definition `json:"reports"`
}

var moduleNames = map[string]string{
	"billing": "Billing and collections", "utilities": "Water", "sales": "Sales", "maintenance": "Maintenance",
	"providers": "Vendors", "gate": "Security",
}

var rangeAndProperty = []string{"range", "property"}

// registry is every report, in screen order.
var registry = []Definition{
	{ID: "collections_month", Name: "Collections by month", Module: "billing", Source: SourceTable, Filters: rangeAndProperty,
		Description: "What was billed against what came in each month, with the collection rate.",
		Subtitle:    "Billed against collected, each month",
		Columns:     []Column{{"period", "Month", "text"}, {"billed", "Billed", "money"}, {"collected", "Collected", "money"}, {"rate", "Collection rate", "pct"}},
		sql:         collectionsMonthSQL},
	{ID: "collections_block", Name: "Collections by block", Module: "billing", Source: SourceTable, Filters: rangeAndProperty,
		Description: "Billed, collected and owing now for each block, to see where arrears sit.",
		Subtitle:    "Billed and collected in the range, and owing now, by block",
		Columns: []Column{{"block", "Block", "text"}, {"units", "Units", "count"}, {"billed", "Billed", "money"}, {"collected", "Collected", "money"},
			{"rate", "Collection rate", "pct"}, {"owing", "Owing now", "money"}},
		sql: collectionsBlockSQL},
	{ID: "collections_charge", Name: "Billing by charge", Module: "billing", Source: SourceTable, Filters: rangeAndProperty,
		Description: "Service charge, water, garbage and other charges billed over the months, with each one's share.",
		Subtitle:    "What was billed, by charge",
		Columns:     []Column{{"charge", "Charge", "text"}, {"billed", "Billed", "money"}, {"share", "Share", "pct"}, {"bills", "Bills", "count"}},
		Note:        "Payments settle the oldest bill as a whole, so collections are not split by charge.",
		sql:         collectionsChargeSQL},
	{ID: "payment_channels", Name: "Payment channels", Module: "billing", Source: SourceTable, Filters: rangeAndProperty,
		Description: "How money came in: M-Pesa prompts and paybill, bank transfers, cheques and cash.",
		Subtitle:    "How money came in",
		Columns:     []Column{{"channel", "Channel", "text"}, {"amount", "Amount", "money"}, {"share", "Share", "pct"}, {"payments", "Payments", "count"}},
		Note:        "M-Pesa covers prompts and paybill payments; the rest are payments recorded by staff or residents and verified.",
		sql:         paymentChannelsSQL},
	{ID: "arrears", Name: "Arrears", Module: "billing", Source: SourceArrears, Filters: []string{"property", "min_owing"},
		Description: "Every account that owes, largest first, with how old the debt is."},
	{ID: "performance", Name: "Estate performance", Module: "billing", Source: SourceFile, Filters: rangeAndProperty,
		Formats:     []string{"pdf", "xlsx"},
		Description: "The chosen months' headline figures against the months before and last year, trends, forecast and revenue mix in one pack for the board."},
	{ID: "water", Name: "Water trend", Module: "utilities", Source: SourceTable, Filters: rangeAndProperty, NeedsProperty: true,
		Description: "Water supplied by the bulk meter and borehole against water billed to units, and the loss.",
		Subtitle:    "Water supplied against billed, each month",
		Columns: []Column{{"period", "Month", "text"}, {"supplied", "Supplied (m3)", "qty"}, {"billed", "Billed (m3)", "qty"},
			{"common", "Common use (m3)", "qty"}, {"lost", "Unaccounted (m3)", "qty"}, {"loss_pct", "Loss", "pct"},
			{"estimated", "Estimated readings", "count"}},
		Note:  "Unaccounted is supplied less billed and common use.",
		build: waterTrend},
	{ID: "sales_position", Permission: "maskani.sales.view", Name: "Sales position", Module: "sales", Source: SourceTable, Filters: []string{"property"},
		Description: "Every live sale: price, paid, balance, what is overdue and the next date, most overdue first.",
		Subtitle:    "Live sale contracts as of today",
		Columns: []Column{{"contract", "Contract", "text"}, {"unit", "Unit", "text"}, {"buyer", "Buyer", "text"}, {"status", "Status", "text"},
			{"price", "Price", "money"}, {"paid", "Paid", "money"}, {"balance", "Balance", "money"}, {"overdue", "Overdue", "money"},
			{"next_due", "Next due", "text"}},
		build: salesPosition},
	{ID: "instalment_receivables", Permission: "maskani.sales.view", Name: "Instalment receivables", Module: "sales", Source: SourceTable, Filters: rangeAndProperty,
		Description: "Instalments by due month: what falls due, what is billed and paid, and what is still owed. Choose months ahead to see what is coming.",
		Subtitle:    "Sale instalments by due month",
		Columns: []Column{{"period", "Month", "text"}, {"instalments", "Instalments", "count"}, {"due", "Falling due", "money"},
			{"billed", "Billed", "money"}, {"paid", "Paid", "money"}, {"owed", "Still owed", "money"}},
		sql: receivablesSQL},
	{ID: "handover_title", Permission: "maskani.sales.view", Name: "Handover and title", Module: "sales", Source: SourceTable, Filters: []string{"property"},
		Description: "Every paid-up sale on its way to title: handover state and date, snags still open and title stages done.",
		Subtitle:    "Fully paid, handed over and titled sales as of today",
		Columns: []Column{{"contract", "Contract", "text"}, {"unit", "Unit", "text"}, {"buyer", "Buyer", "text"}, {"status", "Sale", "text"},
			{"handover", "Handover", "text"}, {"handover_date", "Handover date", "text"}, {"open_snags", "Snags open", "count"},
			{"stages_done", "Title stages done", "count"}, {"stages_blocked", "Blocked", "count"}},
		build: handoverTitle},
	{ID: "maintenance", Name: "Maintenance performance", Module: "maintenance", Source: SourceTable, Filters: rangeAndProperty,
		Description: "Work orders by category: raised, completed, past their time, response and fix times, cost.",
		Subtitle:    "Work orders raised in the range, by category",
		Columns: []Column{{"category", "Category", "text"}, {"opened", "Opened", "count"}, {"completed", "Completed", "count"},
			{"breached", "Past their time", "count"}, {"respond_hours", "Average response", "hours"}, {"resolve_hours", "Average fix", "hours"},
			{"cost", "Cost", "money"}},
		sql: maintenanceSQL},
	{ID: "vendors", Name: "Vendor scorecard", Module: "providers", Source: SourceTable, Filters: rangeAndProperty,
		Description: "Each vendor's jobs, how many were on time, average fix time, cost, and documents expired or expiring.",
		Subtitle:    "Jobs, timeliness, cost and documents, by vendor",
		Columns: []Column{{"vendor", "Vendor", "text"}, {"jobs", "Jobs", "count"}, {"completed", "Completed", "count"},
			{"on_time", "On time", "pct"}, {"resolve_hours", "Average fix", "hours"}, {"cost", "Cost", "money"},
			{"docs_expired", "Documents expired", "count"}, {"docs_expiring", "Expiring in 30 days", "count"}},
		sql: vendorsSQL},
	{ID: "security", Name: "Security summary", Module: "gate", Source: SourceTable, Filters: rangeAndProperty,
		Description: "Gate entries and exits, walk-ins and those turned away, and incidents by month.",
		Subtitle:    "Gate traffic and incidents, each month",
		Columns: []Column{{"period", "Month", "text"}, {"entries", "Entries", "count"}, {"exits", "Exits", "count"},
			{"walk_ins", "Walk-ins", "count"}, {"declined", "Walk-ins declined", "count"}, {"denied", "Turned away", "count"},
			{"incidents", "Incidents", "count"}, {"serious", "Serious incidents", "count"}},
		sql: securitySQL},
}

var byID = func() map[string]Definition {
	m := make(map[string]Definition, len(registry))
	for _, d := range registry {
		if d.Formats == nil {
			d.Formats = []string{"pdf", "xlsx", "csv"}
		}
		d.ExportPath = "/reports/" + d.ID + "/export"
		m[d.ID] = d
	}
	return m
}()

// Lookup returns a report's definition.
func Lookup(id string) (Definition, bool) {
	d, ok := byID[id]
	return d, ok
}

// Catalog returns the reports for a tenant grouped by module: modules it runs (nil means every
// module) and reports the caller may see.
func Catalog(modules map[string]bool, can func(perm string) bool) []CatalogModule {
	out := []CatalogModule{}
	index := map[string]int{}
	for _, r := range registry {
		d := byID[r.ID]
		if (modules != nil && !modules[d.Module]) || (d.Permission != "" && !can(d.Permission)) {
			continue
		}
		i, ok := index[d.Module]
		if !ok {
			index[d.Module] = len(out)
			out = append(out, CatalogModule{Module: d.Module, DisplayName: moduleNames[d.Module], Reports: []Definition{}})
			i = len(out) - 1
		}
		out[i].Reports = append(out[i].Reports, d)
	}
	return out
}
