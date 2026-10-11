package reports

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// Column describes a report column for the screen and the export.
type Column struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Kind is how to show the value: text, money, count, pct, hours, qty.
	Kind string `json:"kind"`
}

// Table is a table report as the screen shows it and the docs engine exports it.
type Table struct {
	Kind     string           `json:"kind"`
	Title    string           `json:"title"`
	Subtitle string           `json:"subtitle"`
	From     string           `json:"from"`
	To       string           `json:"to"`
	Columns  []Column         `json:"columns"`
	Rows     []map[string]any `json:"rows"`
	Totals   map[string]any   `json:"totals,omitempty"`
	Note     string           `json:"note,omitempty"`
}

// ReportRange is a month range, from and to inclusive (YYYY-MM).
type ReportRange struct{ From, To string }

// resolve fills a missing range with the last twelve months and checks it (at most 36 months).
func (rg ReportRange) resolve(now time.Time) (from, to time.Time, err error) {
	if rg.To == "" {
		rg.To = now.Format("2006-01")
	}
	if rg.From == "" {
		t, _ := time.Parse("2006-01", rg.To)
		rg.From = t.AddDate(0, -11, 0).Format("2006-01")
	}
	from, err1 := time.Parse("2006-01", rg.From)
	to, err2 := time.Parse("2006-01", rg.To)
	if err1 != nil || err2 != nil {
		return from, to, httpx.Invalid("from and to are months, YYYY-MM")
	}
	if to.Before(from) || to.Sub(from) > 36*31*24*time.Hour {
		return from, to, httpx.Invalid("choose a range of up to 36 months, from before to")
	}
	return from, to.AddDate(0, 1, 0), nil // to is exclusive from here on
}

// TableReport builds one table report from the registry for the scope and range, cached like the
// dashboard (dropped on payments and billing runs).
func (s *Service) TableReport(ctx context.Context, sc Scope, id string, rg ReportRange) (*Table, error) {
	def, ok := Lookup(id)
	if !ok || def.Source != SourceTable {
		return nil, httpx.Invalid("unknown report")
	}
	from, to, err := rg.resolve(time.Now().In(s.loc))
	if err != nil {
		return nil, err
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	key := fmt.Sprintf("table:%s:%s:%s:%s:%s:%d", id, tenantID, sc.key(), from.Format("2006-01"), to.Format("2006-01"), s.generation(tenantID))
	v, err := s.cached(ctx, key, func() (any, error) { return s.tableReport(ctx, sc, def, from, to) })
	if err != nil {
		return nil, err
	}
	return v.(*Table), nil
}

func (s *Service) tableReport(ctx context.Context, sc Scope, def Definition, from, to time.Time) (*Table, error) {
	t := &Table{Kind: def.ID, Title: def.Name, Subtitle: def.Subtitle, Columns: def.Columns, Note: def.Note,
		From: from.Format("2006-01"), To: to.AddDate(0, -1, 0).Format("2006-01"), Rows: []map[string]any{}}
	if def.build != nil {
		return def.build(ctx, s, sc, t, to)
	}
	if s.db == nil {
		return t, nil
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	return s.fill(ctx, t, def.sql, tenantID, from.Format("2006-01-02"), to.Format("2006-01-02"), sc.sqlIDs())
}

// fill runs a report statement, reads one row per result in the table's column order and adds the
// total row. Range reports and the snapshot reports (with their own parameters) share it.
//
// The statement runs in a short read-only transaction set to the estate's time zone, so a date
// parameter means local midnight and to_char groups timestamps by local month: a work order, gate
// entry or payment at 01:00 on the 1st belongs to that month, as on the dashboard. set_config with
// is_local lasts only for the transaction, so it is safe through PgBouncer's transaction pooling.
func (s *Service) fill(ctx context.Context, t *Table, query string, args ...any) (*Table, error) {
	if s.db == nil {
		return t, nil
	}
	tx, err := s.db.BeginTx(ctx, &stdsql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("%s report: %w", t.Kind, err)
	}
	defer func() { _ = tx.Rollback() }() // read only: nothing to commit
	if _, err := tx.ExecContext(ctx, `SELECT set_config('TimeZone', $1, true)`, pgTimeZone(s.loc, time.Now())); err != nil {
		return nil, fmt.Errorf("%s report time zone: %w", t.Kind, err)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("%s report: %w", t.Kind, err)
	}
	defer rows.Close()
	for rows.Next() {
		vals := make([]any, len(t.Columns))
		ptrs := make([]any, len(t.Columns))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := map[string]any{}
		for i, c := range t.Columns {
			row[c.Key] = cellValue(vals[i], c.Kind)
		}
		t.Rows = append(t.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	t.Totals = totals(t)
	return t, nil
}

// pgTimeZone names loc for Postgres' TimeZone setting: the IANA name when it has one
// (Africa/Nairobi, UTC), else a POSIX fixed offset for the current instant, since a bare
// abbreviation such as the EAT fallback (no tzdata) is not a valid TimeZone. POSIX offsets count
// west of UTC, so UTC+3 is written <+0300>-03:00.
func pgTimeZone(loc *time.Location, now time.Time) string {
	name := loc.String()
	if name == "UTC" || strings.Contains(name, "/") {
		return name
	}
	_, off := now.In(loc).Zone()
	sign, west := "+", -off
	if off < 0 {
		sign = "-"
	}
	abs := off
	if abs < 0 {
		abs = -abs
	}
	return fmt.Sprintf("<%s%02d%02d>%+03d:%02d", sign, abs/3600, abs%3600/60, west/3600, abs%3600/60)
}

// cellValue normalises a scanned value: numbers as decimal strings (money to the cent), counts as
// integers, text as text, NULL as nil.
func cellValue(v any, kind string) any {
	if v == nil {
		return nil
	}
	var d decimal.Decimal
	switch x := v.(type) {
	case []byte:
		if kind == "text" {
			return string(x)
		}
		d, _ = decimal.NewFromString(string(x))
	case string:
		if kind == "text" {
			return x
		}
		d, _ = decimal.NewFromString(x)
	case int64:
		d = decimal.NewFromInt(x)
	case float64:
		d = decimal.NewFromFloat(x)
	default:
		return fmt.Sprint(x)
	}
	switch kind {
	case "count":
		return d.IntPart()
	case "money":
		return d.StringFixed(2)
	default:
		return d.Round(1).String()
	}
}

// totals adds up money and count columns; rates are recomputed where the table has both sides.
func totals(t *Table) map[string]any {
	if len(t.Rows) == 0 {
		return nil
	}
	sum := map[string]decimal.Decimal{}
	for _, r := range t.Rows {
		for _, c := range t.Columns {
			if c.Kind != "money" && c.Kind != "count" {
				continue
			}
			var d decimal.Decimal
			switch x := r[c.Key].(type) {
			case string:
				d, _ = decimal.NewFromString(x)
			case int64:
				d = decimal.NewFromInt(x)
			}
			sum[c.Key] = sum[c.Key].Add(d)
		}
	}
	out := map[string]any{}
	for _, c := range t.Columns {
		if v, ok := sum[c.Key]; ok {
			if c.Kind == "count" {
				out[c.Key] = v.IntPart()
			} else {
				out[c.Key] = v.StringFixed(2)
			}
		}
	}
	if b, c := sum["billed"], sum["collected"]; b.IsPositive() {
		if _, has := sum["collected"]; has {
			out["rate"] = c.Div(b).Mul(decimal.NewFromInt(100)).Round(1).String()
		}
	}
	if _, has := sum["amount"]; has {
		out["share"] = "100"
	}
	return out
}

// waterTrend reuses the water balance (supplied against billed) for one property.
func waterTrend(ctx context.Context, s *Service, sc Scope, t *Table, to time.Time) (*Table, error) {
	if sc.PropertyID == nil {
		return nil, httpx.Invalid("choose a property for the water trend")
	}
	points, err := s.utilities.WaterBalance(ctx, *sc.PropertyID, to.AddDate(0, -1, 0).Format("2006-01"))
	if err != nil {
		return nil, err
	}
	for _, p := range points {
		if p.Period < t.From {
			continue
		}
		t.Rows = append(t.Rows, map[string]any{"period": p.Period, "supplied": p.Supplied.Round(1).String(), "billed": p.Billed.Round(1).String(),
			"common": p.Common.Round(1).String(), "lost": p.Lost.Round(1).String(), "loss_pct": p.LossPct.Round(1).String(),
			"estimated": int64(p.Estimated)})
	}
	return t, nil
}
