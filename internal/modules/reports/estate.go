package reports

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// Estate reports (wave 2.3): one table per question an estate office asks, each built by one
// grouped statement on the read replica and cached like the dashboard, so it costs the same at 40
// units or 40,000. The same table is shown on screen and exported through the docs engine.

// Column describes a report column for the screen and the export.
type Column struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Kind is how to show the value: text, money, count, pct, hours, qty.
	Kind string `json:"kind"`
}

// Table is one estate report.
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

// ReportKind names one estate report.
type ReportKind struct {
	Kind  string `json:"kind"`
	Title string `json:"title"`
}

// EstateReportKinds are the reports in screen order.
var EstateReportKinds = []ReportKind{
	{"collections_month", "Collections by month"},
	{"collections_block", "Collections by block"},
	{"collections_charge", "Billing by charge"},
	{"payment_channels", "Payment channels"},
	{"maintenance", "Maintenance performance"},
	{"vendors", "Vendor scorecard"},
	{"security", "Security summary"},
	{"water", "Water trend"},
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

// EstateReport builds one report for the scope and range.
func (s *Service) EstateReport(ctx context.Context, sc Scope, kind string, rg ReportRange) (*Table, error) {
	from, to, err := rg.resolve(time.Now().In(s.loc))
	if err != nil {
		return nil, err
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	key := fmt.Sprintf("estate:%s:%s:%s:%s:%s:%d", kind, tenantID, sc.key(), from.Format("2006-01"), to.Format("2006-01"), s.generation(tenantID))
	v, err := s.cached(ctx, key, func() (any, error) { return s.estateReport(ctx, sc, kind, from, to) })
	if err != nil {
		return nil, err
	}
	return v.(*Table), nil
}

func (s *Service) estateReport(ctx context.Context, sc Scope, kind string, from, to time.Time) (*Table, error) {
	t := &Table{Kind: kind, From: from.Format("2006-01"), To: to.AddDate(0, -1, 0).Format("2006-01"), Rows: []map[string]any{}}
	for _, k := range EstateReportKinds {
		if k.Kind == kind {
			t.Title = k.Title
		}
	}
	if t.Title == "" {
		return nil, httpx.Invalid("unknown report")
	}
	if s.db == nil {
		return t, nil
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	ids := sc.sqlIDs()
	args := []any{tenantID, from.Format("2006-01-02"), to.Format("2006-01-02"), ids}
	var q string
	switch kind {
	case "collections_month":
		t.Subtitle = "Billed against collected, each month"
		t.Columns = []Column{{"period", "Month", "text"}, {"billed", "Billed", "money"}, {"collected", "Collected", "money"}, {"rate", "Collection rate", "pct"}}
		q = collectionsMonthSQL
	case "collections_block":
		t.Subtitle = "Billed and collected in the range, and owing now, by block"
		t.Columns = []Column{{"block", "Block", "text"}, {"units", "Units", "count"}, {"billed", "Billed", "money"}, {"collected", "Collected", "money"},
			{"rate", "Collection rate", "pct"}, {"owing", "Owing now", "money"}}
		q = collectionsBlockSQL
	case "collections_charge":
		t.Subtitle = "What was billed, by charge"
		t.Columns = []Column{{"charge", "Charge", "text"}, {"billed", "Billed", "money"}, {"share", "Share", "pct"}, {"bills", "Bills", "count"}}
		t.Note = "Payments settle the oldest bill as a whole, so collections are not split by charge."
		q = collectionsChargeSQL
	case "payment_channels":
		t.Subtitle = "How money came in"
		t.Columns = []Column{{"channel", "Channel", "text"}, {"amount", "Amount", "money"}, {"share", "Share", "pct"}, {"payments", "Payments", "count"}}
		t.Note = "M-Pesa covers prompts and paybill payments; the rest are payments recorded by staff or residents and verified."
		q = paymentChannelsSQL
	case "maintenance":
		t.Subtitle = "Work orders raised in the range, by category"
		t.Columns = []Column{{"category", "Category", "text"}, {"opened", "Opened", "count"}, {"completed", "Completed", "count"},
			{"breached", "Past their time", "count"}, {"respond_hours", "Average response", "hours"}, {"resolve_hours", "Average fix", "hours"},
			{"cost", "Cost", "money"}}
		q = maintenanceSQL
	case "vendors":
		t.Subtitle = "Jobs, timeliness, cost and documents, by vendor"
		t.Columns = []Column{{"vendor", "Vendor", "text"}, {"jobs", "Jobs", "count"}, {"completed", "Completed", "count"},
			{"on_time", "On time", "pct"}, {"resolve_hours", "Average fix", "hours"}, {"cost", "Cost", "money"},
			{"docs_expired", "Documents expired", "count"}, {"docs_expiring", "Expiring in 30 days", "count"}}
		q = vendorsSQL
	case "security":
		t.Subtitle = "Gate traffic and incidents, each month"
		t.Columns = []Column{{"period", "Month", "text"}, {"entries", "Entries", "count"}, {"exits", "Exits", "count"},
			{"walk_ins", "Walk-ins", "count"}, {"declined", "Walk-ins declined", "count"}, {"denied", "Turned away", "count"},
			{"incidents", "Incidents", "count"}, {"serious", "Serious incidents", "count"}}
		q = securitySQL
	case "water":
		return s.waterTrend(ctx, sc, t, to)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("%s report: %w", kind, err)
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
func (s *Service) waterTrend(ctx context.Context, sc Scope, t *Table, to time.Time) (*Table, error) {
	if sc.PropertyID == nil {
		return nil, httpx.Invalid("choose a property for the water trend")
	}
	t.Subtitle = "Water supplied against billed, each month"
	t.Columns = []Column{{"period", "Month", "text"}, {"supplied", "Supplied (m3)", "qty"}, {"billed", "Billed (m3)", "qty"},
		{"lost", "Unaccounted (m3)", "qty"}, {"loss_pct", "Loss", "pct"}}
	points, err := s.utilities.WaterBalance(ctx, *sc.PropertyID, to.AddDate(0, -1, 0).Format("2006-01"))
	if err != nil {
		return nil, err
	}
	for _, p := range points {
		if p.Period < t.From {
			continue
		}
		t.Rows = append(t.Rows, map[string]any{"period": p.Period, "supplied": p.Supplied.Round(1).String(), "billed": p.Billed.Round(1).String(),
			"lost": p.Lost.Round(1).String(), "loss_pct": p.LossPct.Round(1).String()})
	}
	return t, nil
}

// Every statement takes $1 tenant, $2 first day (date), $3 the day after the range (date), and
// $4 the property ids literal or NULL for the whole tenant.

const collectionsMonthSQL = `
WITH months AS (
  SELECT to_char(gs, 'YYYY-MM') AS p FROM generate_series($2::date, ($3::date - interval '1 month'), interval '1 month') gs
), billed AS (
  SELECT r.period AS p, SUM(l.total) AS amt
    FROM billing_run_lines l JOIN billing_runs r ON r.id = l.run_id AND r.tenant_id = $1
   WHERE l.tenant_id = $1 AND l.status = 'issued' AND r.period >= to_char($2::date, 'YYYY-MM') AND r.period < to_char($3::date, 'YYYY-MM')
     AND ($4::text IS NULL OR r.property_id = ANY($4::text::uuid[]))
   GROUP BY 1
), coll AS (
  SELECT to_char(d.day, 'YYYY-MM') AS p, SUM(d.collected) AS amt
    FROM daily_stats d
   WHERE d.tenant_id = $1 AND d.day >= $2::date AND d.day < $3::date
     AND ($4::text IS NULL OR d.property_id = ANY($4::text::uuid[]))
   GROUP BY 1
)
SELECT m.p, COALESCE(b.amt, 0), COALESCE(c.amt, 0),
       CASE WHEN COALESCE(b.amt, 0) > 0 THEN ROUND(COALESCE(c.amt, 0) * 100 / b.amt, 1) END
  FROM months m LEFT JOIN billed b ON b.p = m.p LEFT JOIN coll c ON c.p = m.p
 ORDER BY m.p`

const collectionsBlockSQL = `
WITH bu AS (
  SELECT u.id, u.block_id FROM units u
   WHERE u.tenant_id = $1 AND u.status = 'active' AND ($4::text IS NULL OR u.property_id = ANY($4::text::uuid[]))
), n AS (
  SELECT block_id, COUNT(*) AS units FROM bu GROUP BY 1
), billed AS (
  SELECT bu.block_id, SUM(l.total) AS amt
    FROM billing_run_lines l JOIN billing_runs r ON r.id = l.run_id AND r.tenant_id = $1 JOIN bu ON bu.id = l.unit_id
   WHERE l.tenant_id = $1 AND l.status = 'issued' AND r.period >= to_char($2::date, 'YYYY-MM') AND r.period < to_char($3::date, 'YYYY-MM')
   GROUP BY 1
), coll AS (
  SELECT bu.block_id, SUM(ac.amount) AS amt
    FROM account_collections ac JOIN bu ON bu.id = ac.unit_id
   WHERE ac.tenant_id = $1 AND ac.day >= $2::date AND ac.day < $3::date
   GROUP BY 1
), owing AS (
  SELECT bu.block_id, SUM(ua.balance) AS amt
    FROM unit_accounts ua JOIN bu ON bu.id = ua.unit_id
   WHERE ua.tenant_id = $1 AND ua.status = 'active' AND ua.balance > 0
   GROUP BY 1
)
SELECT COALESCE(b.name, 'No block'), n.units, COALESCE(bi.amt, 0), COALESCE(c.amt, 0),
       CASE WHEN COALESCE(bi.amt, 0) > 0 THEN ROUND(COALESCE(c.amt, 0) * 100 / bi.amt, 1) END, COALESCE(o.amt, 0)
  FROM n LEFT JOIN blocks b ON b.id = n.block_id
  LEFT JOIN billed bi ON bi.block_id IS NOT DISTINCT FROM n.block_id
  LEFT JOIN coll c ON c.block_id IS NOT DISTINCT FROM n.block_id
  LEFT JOIN owing o ON o.block_id IS NOT DISTINCT FROM n.block_id
 ORDER BY 1`

const collectionsChargeSQL = `
WITH lines AS (
  SELECT x->>'charge_code' AS code, NULLIF(x->>'amount', '')::numeric AS amt, l.id AS line_id
    FROM billing_run_lines l JOIN billing_runs r ON r.id = l.run_id AND r.tenant_id = $1,
         jsonb_array_elements(l.lines) x
   WHERE l.tenant_id = $1 AND l.status = 'issued' AND r.period >= to_char($2::date, 'YYYY-MM') AND r.period < to_char($3::date, 'YYYY-MM')
     AND ($4::text IS NULL OR r.property_id = ANY($4::text::uuid[]))
), g AS (
  SELECT code, SUM(amt) AS amt, COUNT(DISTINCT line_id) AS bills FROM lines GROUP BY 1
)
SELECT COALESCE(ct.name, g.code), g.amt, ROUND(g.amt * 100 / NULLIF(SUM(g.amt) OVER (), 0), 1), g.bills
  FROM g LEFT JOIN charge_types ct ON ct.tenant_id = $1 AND ct.code = g.code
 ORDER BY g.amt DESC`

const paymentChannelsSQL = `
WITH total AS (
  SELECT COALESCE(SUM(d.collected), 0) AS amt, COALESCE(SUM(d.payments_count), 0) AS n
    FROM daily_stats d
   WHERE d.tenant_id = $1 AND d.day >= $2::date AND d.day < $3::date
     AND ($4::text IS NULL OR d.property_id = ANY($4::text::uuid[]))
), manual AS (
  SELECT CASE m.method WHEN 'bank_transfer' THEN 'Bank transfer' WHEN 'cheque' THEN 'Cheque' WHEN 'cash' THEN 'Cash'
                       ELSE 'M-Pesa code entered by staff' END AS channel,
         SUM(m.amount) AS amt, COUNT(*) AS n
    FROM manual_payments m
   WHERE m.tenant_id = $1 AND m.status = 'approved' AND m.reviewed_at >= $2::date AND m.reviewed_at < $3::date
     AND ($4::text IS NULL OR m.property_id = ANY($4::text::uuid[]))
   GROUP BY 1
), rows AS (
  SELECT 'M-Pesa (prompt and paybill)' AS channel,
         GREATEST(t.amt - COALESCE((SELECT SUM(amt) FROM manual), 0), 0) AS amt,
         GREATEST(t.n - COALESCE((SELECT SUM(n) FROM manual), 0), 0) AS n
    FROM total t
  UNION ALL SELECT channel, amt, n FROM manual
)
SELECT channel, amt, ROUND(amt * 100 / NULLIF(SUM(amt) OVER (), 0), 1), n FROM rows ORDER BY amt DESC`

const maintenanceSQL = `
SELECT w.category, COUNT(*),
       COUNT(*) FILTER (WHERE w.completed_at IS NOT NULL),
       COUNT(*) FILTER (WHERE w.sla_breached),
       ROUND((AVG(EXTRACT(EPOCH FROM (w.responded_at - w.created_at))) FILTER (WHERE w.responded_at IS NOT NULL) / 3600)::numeric, 1),
       ROUND((AVG(EXTRACT(EPOCH FROM (w.completed_at - w.created_at))) FILTER (WHERE w.completed_at IS NOT NULL) / 3600)::numeric, 1),
       COALESCE(SUM(w.cost_amount), 0)
  FROM work_orders w
 WHERE w.tenant_id = $1 AND w.created_at >= $2::date AND w.created_at < $3::date AND w.status <> 'cancelled'
   AND ($4::text IS NULL OR w.property_id = ANY($4::text::uuid[]))
 GROUP BY 1
 ORDER BY 2 DESC`

const vendorsSQL = `
WITH jobs AS (
  SELECT w.vendor_id, COUNT(*) AS jobs,
         COUNT(*) FILTER (WHERE w.completed_at IS NOT NULL) AS done,
         COUNT(*) FILTER (WHERE w.completed_at IS NOT NULL AND NOT w.sla_breached) AS on_time,
         AVG(EXTRACT(EPOCH FROM (w.completed_at - w.created_at))) FILTER (WHERE w.completed_at IS NOT NULL) / 3600 AS hours,
         COALESCE(SUM(w.cost_amount), 0) AS cost
    FROM work_orders w
   WHERE w.tenant_id = $1 AND w.vendor_id IS NOT NULL AND w.created_at >= $2::date AND w.created_at < $3::date AND w.status <> 'cancelled'
     AND ($4::text IS NULL OR w.property_id = ANY($4::text::uuid[]))
   GROUP BY 1
), docs AS (
  SELECT d.vendor_id, COUNT(*) FILTER (WHERE d.expires_at < now()) AS expired,
         COUNT(*) FILTER (WHERE d.expires_at >= now() AND d.expires_at < now() + interval '30 days') AS expiring
    FROM vendor_documents d WHERE d.tenant_id = $1 GROUP BY 1
)
SELECT v.name, COALESCE(j.jobs, 0), COALESCE(j.done, 0),
       CASE WHEN COALESCE(j.done, 0) > 0 THEN ROUND(j.on_time * 100.0 / j.done, 1) END,
       ROUND(j.hours::numeric, 1), COALESCE(j.cost, 0), COALESCE(d.expired, 0), COALESCE(d.expiring, 0)
  FROM vendors v LEFT JOIN jobs j ON j.vendor_id = v.id LEFT JOIN docs d ON d.vendor_id = v.id
 WHERE v.tenant_id = $1 AND v.status = 'active'
 ORDER BY 2 DESC, 1`

const securitySQL = `
WITH months AS (
  SELECT to_char(gs, 'YYYY-MM') AS p FROM generate_series($2::date, ($3::date - interval '1 month'), interval '1 month') gs
), gate AS (
  SELECT to_char(g.occurred_at, 'YYYY-MM') AS p,
         COUNT(*) FILTER (WHERE g.kind = 'entry') AS entries,
         COUNT(*) FILTER (WHERE g.kind = 'exit') AS exits,
         COUNT(*) FILTER (WHERE g.kind = 'walk_in_request') AS walk_ins,
         COUNT(*) FILTER (WHERE g.kind = 'walk_in_request' AND g.decision = 'declined') AS declined,
         COUNT(*) FILTER (WHERE g.kind = 'denied') AS denied
    FROM gate_events g
   WHERE g.tenant_id = $1 AND g.occurred_at >= $2::date AND g.occurred_at < $3::date
     AND ($4::text IS NULL OR g.property_id = ANY($4::text::uuid[]))
   GROUP BY 1
), inc AS (
  SELECT to_char(i.occurred_at, 'YYYY-MM') AS p, COUNT(*) AS n, COUNT(*) FILTER (WHERE i.severity IN ('high', 'critical')) AS serious
    FROM incidents i
   WHERE i.tenant_id = $1 AND i.occurred_at >= $2::date AND i.occurred_at < $3::date
     AND ($4::text IS NULL OR i.property_id = ANY($4::text::uuid[]))
   GROUP BY 1
)
SELECT m.p, COALESCE(g.entries, 0), COALESCE(g.exits, 0), COALESCE(g.walk_ins, 0), COALESCE(g.declined, 0), COALESCE(g.denied, 0),
       COALESCE(i.n, 0), COALESCE(i.serious, 0)
  FROM months m LEFT JOIN gate g ON g.p = m.p LEFT JOIN inc i ON i.p = m.p
 ORDER BY m.p`
