package reports

// The report catalog is the one list of every report Maskani offers, grouped by module, the same
// pattern as TruLoad's /reports/catalog: the console's single Reports page reads it, shows only the
// modules the tenant runs and the reports the caller may see, and generates each one the same way.

// CatalogReport describes one report.
type CatalogReport struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Module      string `json:"module"`
	// Source says how the screen shows it: estate (a table from /reports/estate/{id}), arrears
	// (the keyset arrears list) or file (download only).
	Source string `json:"source"`
	// Filters the report takes: range (from and to months), property, period (one month), min_owing.
	Filters []string `json:"filters"`
	Formats []string `json:"formats"`
	// ExportPath is the download path under the tenant base, the format added as ?format=.
	ExportPath string `json:"export_path"`
	// Permission beyond reports.view needed to see it, when any.
	Permission string `json:"permission,omitempty"`
	// NeedsProperty is true when the report covers one property only.
	NeedsProperty bool `json:"needs_property,omitempty"`
}

// CatalogModule is one module's reports.
type CatalogModule struct {
	Module      string          `json:"module"`
	DisplayName string          `json:"display_name"`
	Reports     []CatalogReport `json:"reports"`
}

var allFormats = []string{"pdf", "xlsx", "csv"}

// catalog is every report, in screen order. The module is the Maskani module that must be on.
var catalog = []CatalogModule{
	{Module: "billing", DisplayName: "Billing and collections", Reports: []CatalogReport{
		{ID: "collections_month", Name: "Collections by month", Description: "What was billed against what came in each month, with the collection rate.",
			Source: "estate", Filters: []string{"range", "property"}},
		{ID: "collections_block", Name: "Collections by block", Description: "Billed, collected and owing now for each block, to see where arrears sit.",
			Source: "estate", Filters: []string{"range", "property"}},
		{ID: "collections_charge", Name: "Billing by charge", Description: "Service charge, water, garbage and other charges billed over the months, with each one's share.",
			Source: "estate", Filters: []string{"range", "property"}},
		{ID: "payment_channels", Name: "Payment channels", Description: "How money came in: M-Pesa prompts and paybill, bank transfers, cheques and cash.",
			Source: "estate", Filters: []string{"range", "property"}},
		{ID: "arrears", Name: "Arrears", Description: "Every account that owes, largest first, with how old the debt is.",
			Source: "arrears", Filters: []string{"property", "min_owing"}, ExportPath: "/reports/arrears/export"},
		{ID: "performance", Name: "Estate performance", Description: "The month's headline figures, trends, forecast and revenue mix in one pack for the board.",
			Source: "file", Filters: []string{"period", "property"}, ExportPath: "/reports/insights/export", Formats: []string{"pdf", "xlsx"}},
	}},
	{Module: "utilities", DisplayName: "Water", Reports: []CatalogReport{
		{ID: "water", Name: "Water trend", Description: "Water supplied by the bulk meter and borehole against water billed to units, and the loss.",
			Source: "estate", Filters: []string{"range", "property"}, NeedsProperty: true},
	}},
	{Module: "maintenance", DisplayName: "Maintenance", Reports: []CatalogReport{
		{ID: "maintenance", Name: "Maintenance performance", Description: "Work orders by category: raised, completed, past their time, response and fix times, cost.",
			Source: "estate", Filters: []string{"range", "property"}},
	}},
	{Module: "providers", DisplayName: "Vendors", Reports: []CatalogReport{
		{ID: "vendors", Name: "Vendor scorecard", Description: "Each vendor's jobs, how many were on time, average fix time, cost, and documents expired or expiring.",
			Source: "estate", Filters: []string{"range", "property"}},
	}},
	{Module: "gate", DisplayName: "Security", Reports: []CatalogReport{
		{ID: "security", Name: "Security summary", Description: "Gate entries and exits, walk-ins and those turned away, and incidents by month.",
			Source: "estate", Filters: []string{"range", "property"}},
	}},
}

// Catalog returns the report catalog for a tenant: modules it runs (modules is the tenant's
// module switches; nil means every module) and reports the caller may see.
func Catalog(modules map[string]bool, can func(perm string) bool) []CatalogModule {
	out := []CatalogModule{}
	for _, m := range catalog {
		if modules != nil && !modules[m.Module] {
			continue
		}
		mod := CatalogModule{Module: m.Module, DisplayName: m.DisplayName, Reports: []CatalogReport{}}
		for _, r := range m.Reports {
			if r.Permission != "" && !can(r.Permission) {
				continue
			}
			r.Module = m.Module
			if r.Formats == nil {
				r.Formats = allFormats
			}
			if r.ExportPath == "" && r.Source == "estate" {
				r.ExportPath = "/reports/estate/" + r.ID + "/export"
			}
			mod.Reports = append(mod.Reports, r)
		}
		if len(mod.Reports) > 0 {
			out = append(out, mod)
		}
	}
	return out
}
