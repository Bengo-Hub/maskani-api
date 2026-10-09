package reports

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// Insights is the business view behind the staff dashboard: trends, comparisons and forecasts the
// estate can plan with. Every figure comes from grouped SQL on the read replica (one statement per
// block below), cached like the dashboard, so it costs the same at 40 units or 40,000.
type Insights struct {
	Period   string        `json:"period"`
	Months   []MonthPoint  `json:"months"`
	KPIs     InsightKPIs   `json:"kpis"`
	Forecast []ForecastRow `json:"forecast"`
	// ForecastBasis says how the forecast was built, so the screen can show its method.
	ForecastBasis ForecastBasis     `json:"forecast_basis"`
	Sales         SalesOutlook      `json:"sales"`
	RevenueMix    []MixRow          `json:"revenue_mix"`
	Blocks        []BlockRow        `json:"blocks"`
	WorkByType    []WorkCategoryRow `json:"work_by_category"`
}

// MonthPoint is one month of the trend lines.
type MonthPoint struct {
	Period          string           `json:"period"`
	Billed          decimal.Decimal  `json:"billed"`
	Collected       decimal.Decimal  `json:"collected"`
	CollectionRate  *decimal.Decimal `json:"collection_rate,omitempty"`
	WorkOpened      int              `json:"work_opened"`
	WorkClosed      int              `json:"work_closed"`
	ContractsSigned int              `json:"contracts_signed"`
	SalesValue      decimal.Decimal  `json:"sales_value"`
}

// KPI is a figure with the comparisons a manager asks for first.
type KPI struct {
	Value     decimal.Decimal  `json:"value"`
	LastMonth *decimal.Decimal `json:"last_month,omitempty"`
	LastYear  *decimal.Decimal `json:"last_year,omitempty"`
}

// InsightKPIs are the headline numbers.
type InsightKPIs struct {
	CollectionRate KPI             `json:"collection_rate"`
	Billed         KPI             `json:"billed"`
	Collected      KPI             `json:"collected"`
	Outstanding    decimal.Decimal `json:"outstanding"`
	// DaysSalesOutstanding is what is owed now divided by average daily billing over 90 days.
	DaysSalesOutstanding *decimal.Decimal `json:"days_sales_outstanding,omitempty"`
	Units                int              `json:"units"`
	Occupied             int              `json:"occupied"`
	OccupancyPct         *decimal.Decimal `json:"occupancy_pct,omitempty"`
	Available            int              `json:"available_for_sale"`
	OpenWorkOrders       int              `json:"open_work_orders"`
	AvgResolveHours      *decimal.Decimal `json:"avg_resolve_hours_90d,omitempty"`
}

// ForecastRow is one future month of expected cash in.
type ForecastRow struct {
	Period      string          `json:"period"`
	Instalments decimal.Decimal `json:"instalments"`
	Recurring   decimal.Decimal `json:"recurring"`
	Total       decimal.Decimal `json:"total"`
}

// ForecastBasis is the inputs behind the forecast.
type ForecastBasis struct {
	AvgMonthlyBilled   decimal.Decimal `json:"avg_monthly_billed_3m"`
	CollectionRate6m   decimal.Decimal `json:"collection_rate_6m"`
	OverdueInstalments decimal.Decimal `json:"overdue_instalments"`
	Method             string          `json:"method"`
}

// SalesOutlook is the pace of unit sales.
type SalesOutlook struct {
	SignedLast6m      int              `json:"signed_last_6m"`
	MonthlyRate       decimal.Decimal  `json:"monthly_rate"`
	Available         int              `json:"available"`
	MonthsToSellOut   *decimal.Decimal `json:"months_to_sell_out,omitempty"`
	ContractValue     decimal.Decimal  `json:"contract_value"`
	ContractCollected decimal.Decimal  `json:"contract_collected"`
}

// MixRow is billed revenue by charge over the last three complete months.
type MixRow struct {
	ChargeCode string          `json:"charge_code"`
	Amount     decimal.Decimal `json:"amount"`
}

// BlockRow compares blocks: units, last complete month billed, owing now.
type BlockRow struct {
	BlockID uuid.UUID       `json:"block_id"`
	Name    string          `json:"name"`
	Units   int             `json:"units"`
	Billed  decimal.Decimal `json:"billed_last_month"`
	Owing   decimal.Decimal `json:"owing"`
}

// WorkCategoryRow is maintenance performance by category over 90 days.
type WorkCategoryRow struct {
	Category        string           `json:"category"`
	Opened          int              `json:"opened"`
	Completed       int              `json:"completed"`
	Breached        int              `json:"breached"`
	AvgResolveHours *decimal.Decimal `json:"avg_resolve_hours,omitempty"`
}

const insightsTTLKey = "insights"

// Insights builds the business view for a scope and month (default this month), cached 60 seconds
// per tenant, scope and period and dropped on payments and billing runs.
func (s *Service) Insights(ctx context.Context, sc Scope, period string) (*Insights, error) {
	if period == "" {
		period = time.Now().In(s.loc).Format("2006-01")
	}
	start, err := time.ParseInLocation("2006-01", period, s.loc)
	if err != nil {
		return nil, fmt.Errorf("period must be YYYY-MM")
	}
	v, err := s.cached(ctx, insightsTTLKey+":"+period+":"+sc.key(), func() (any, error) { return s.insights(ctx, sc, period, start) })
	if err != nil {
		return nil, err
	}
	return v.(*Insights), nil
}

// monthsSQL: 24 months of billed (issued run lines by run period), collected (daily_stats), work
// orders opened and closed, and contracts signed. $1 tenant, $2 first month (date), $3 month after
// the last (date), $4 property ids literal or NULL, $5 time zone.
const monthsSQL = `
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
), wo_open AS (
  SELECT to_char(w.created_at AT TIME ZONE $5, 'YYYY-MM') AS p, COUNT(*) AS n
    FROM work_orders w
   WHERE w.tenant_id = $1 AND w.created_at >= $2::date AND w.created_at < $3::date
     AND ($4::text IS NULL OR w.property_id = ANY($4::text::uuid[]))
   GROUP BY 1
), wo_done AS (
  SELECT to_char(w.completed_at AT TIME ZONE $5, 'YYYY-MM') AS p, COUNT(*) AS n
    FROM work_orders w
   WHERE w.tenant_id = $1 AND w.completed_at >= $2::date AND w.completed_at < $3::date
     AND ($4::text IS NULL OR w.property_id = ANY($4::text::uuid[]))
   GROUP BY 1
), signed AS (
  SELECT to_char(c.signed_at AT TIME ZONE $5, 'YYYY-MM') AS p, COUNT(*) AS n, SUM(c.net_price) AS v
    FROM sale_contracts c
   WHERE c.tenant_id = $1 AND c.signed_at >= $2::date AND c.signed_at < $3::date
     AND c.status NOT IN ('draft', 'cancelled')
     AND ($4::text IS NULL OR c.property_id = ANY($4::text::uuid[]))
   GROUP BY 1
)
SELECT m.p, COALESCE(b.amt, 0), COALESCE(c.amt, 0), COALESCE(o.n, 0), COALESCE(d.n, 0), COALESCE(s.n, 0), COALESCE(s.v, 0)
  FROM months m
  LEFT JOIN billed b ON b.p = m.p LEFT JOIN coll c ON c.p = m.p LEFT JOIN wo_open o ON o.p = m.p
  LEFT JOIN wo_done d ON d.p = m.p LEFT JOIN signed s ON s.p = m.p
 ORDER BY m.p`

// positionSQL: the position now. $1 tenant, $2 property ids literal or NULL.
const positionSQL = `
SELECT
  (SELECT COALESCE(SUM(ua.balance), 0) FROM unit_accounts ua JOIN units u ON u.id = ua.unit_id AND u.tenant_id = $1
    WHERE ua.tenant_id = $1 AND ua.status = 'active' AND ua.balance > 0 AND ($2::text IS NULL OR u.property_id = ANY($2::text::uuid[]))),
  (SELECT COUNT(*) FROM units u WHERE u.tenant_id = $1 AND u.status = 'active' AND ($2::text IS NULL OR u.property_id = ANY($2::text::uuid[]))),
  (SELECT COUNT(*) FROM units u WHERE u.tenant_id = $1 AND u.status = 'active' AND u.occupancy_status IN ('owner_occupied','tenanted')
    AND ($2::text IS NULL OR u.property_id = ANY($2::text::uuid[]))),
  (SELECT COUNT(*) FROM units u WHERE u.tenant_id = $1 AND u.status = 'active' AND u.sale_status = 'available'
    AND ($2::text IS NULL OR u.property_id = ANY($2::text::uuid[]))),
  (SELECT COUNT(*) FROM work_orders w WHERE w.tenant_id = $1 AND w.status NOT IN ('completed','confirmed','closed','cancelled')
    AND ($2::text IS NULL OR w.property_id = ANY($2::text::uuid[]))),
  (SELECT AVG(EXTRACT(EPOCH FROM (w.completed_at - w.created_at)) / 3600) FROM work_orders w
    WHERE w.tenant_id = $1 AND w.completed_at >= now() - interval '90 days'
      AND ($2::text IS NULL OR w.property_id = ANY($2::text::uuid[]))),
  (SELECT COALESCE(SUM(c.net_price), 0) FROM sale_contracts c WHERE c.tenant_id = $1 AND c.status NOT IN ('draft','cancelled','terminated')
    AND ($2::text IS NULL OR c.property_id = ANY($2::text::uuid[]))),
  (SELECT COALESCE(SUM(c.paid_total), 0) FROM sale_contracts c WHERE c.tenant_id = $1 AND c.status NOT IN ('draft','cancelled','terminated')
    AND ($2::text IS NULL OR c.property_id = ANY($2::text::uuid[]))),
  (SELECT COALESCE(SUM(i.amount - i.paid_amount), 0) FROM instalments i JOIN sale_contracts c ON c.id = i.contract_id AND c.tenant_id = $1
    WHERE i.tenant_id = $1 AND i.status NOT IN ('paid','waived') AND i.due_date < now()
      AND c.status NOT IN ('draft','cancelled','terminated') AND ($2::text IS NULL OR c.property_id = ANY($2::text::uuid[])))`

// instalmentForecastSQL: scheduled instalment money still to come, by due month. $1 tenant, $2 from
// (date), $3 to (date), $4 property ids literal or NULL, $5 time zone.
const instalmentForecastSQL = `
SELECT to_char(i.due_date AT TIME ZONE $5, 'YYYY-MM'), COALESCE(SUM(i.amount - i.paid_amount), 0)
  FROM instalments i JOIN sale_contracts c ON c.id = i.contract_id AND c.tenant_id = $1
 WHERE i.tenant_id = $1 AND i.status NOT IN ('paid','waived') AND i.due_date >= $2::date AND i.due_date < $3::date
   AND c.status NOT IN ('draft','cancelled','terminated') AND ($4::text IS NULL OR c.property_id = ANY($4::text::uuid[]))
 GROUP BY 1`

// mixSQL: billed revenue by charge code over a run-period range. $1 tenant, $2 first period,
// $3 period after the last, $4 property ids literal or NULL.
const mixSQL = `
SELECT COALESCE(NULLIF(e->>'charge_code', ''), 'other'), SUM((e->>'amount')::numeric)
  FROM billing_run_lines l JOIN billing_runs r ON r.id = l.run_id AND r.tenant_id = $1
  CROSS JOIN LATERAL jsonb_array_elements(l.lines) e
 WHERE l.tenant_id = $1 AND l.status = 'issued' AND r.period >= $2 AND r.period < $3
   AND ($4::text IS NULL OR r.property_id = ANY($4::text::uuid[]))
 GROUP BY 1 ORDER BY 2 DESC LIMIT 20`

// blocksSQL: per block, units, last month billed and owing now. $1 tenant, $2 period, $3 ids.
const blocksSQL = `
WITH u AS (
  SELECT u.id, u.block_id FROM units u
   WHERE u.tenant_id = $1 AND u.status = 'active' AND u.block_id IS NOT NULL
     AND ($3::text IS NULL OR u.property_id = ANY($3::text::uuid[]))
), billed AS (
  SELECT u.block_id, SUM(l.total) AS amt FROM billing_run_lines l
    JOIN billing_runs r ON r.id = l.run_id AND r.tenant_id = $1 JOIN u ON u.id = l.unit_id
   WHERE l.tenant_id = $1 AND l.status = 'issued' AND r.period = $2 GROUP BY 1
), owing AS (
  SELECT u.block_id, SUM(ua.balance) AS amt FROM unit_accounts ua JOIN u ON u.id = ua.unit_id
   WHERE ua.tenant_id = $1 AND ua.status = 'active' AND ua.balance > 0 GROUP BY 1
)
SELECT b.id, b.name, COUNT(u.id), COALESCE(MAX(bl.amt), 0), COALESCE(MAX(o.amt), 0)
  FROM blocks b JOIN u ON u.block_id = b.id
  LEFT JOIN billed bl ON bl.block_id = b.id LEFT JOIN owing o ON o.block_id = b.id
 WHERE b.tenant_id = $1
 GROUP BY b.id, b.name ORDER BY b.name LIMIT 100`

// workSQL: maintenance by category over 90 days. $1 tenant, $2 property ids literal or NULL.
const workSQL = `
SELECT w.category, COUNT(*),
       COUNT(*) FILTER (WHERE w.completed_at IS NOT NULL),
       COUNT(*) FILTER (WHERE w.sla_breached),
       AVG(EXTRACT(EPOCH FROM (w.completed_at - w.created_at)) / 3600) FILTER (WHERE w.completed_at IS NOT NULL)
  FROM work_orders w
 WHERE w.tenant_id = $1 AND w.created_at >= now() - interval '90 days'
   AND ($2::text IS NULL OR w.property_id = ANY($2::text::uuid[]))
 GROUP BY 1 ORDER BY 2 DESC LIMIT 20`

func (s *Service) insights(ctx context.Context, sc Scope, period string, start time.Time) (*Insights, error) {
	out := &Insights{Period: period, Months: []MonthPoint{}, Forecast: []ForecastRow{}, RevenueMix: []MixRow{},
		Blocks: []BlockRow{}, WorkByType: []WorkCategoryRow{}}
	if s.db == nil {
		return out, nil
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	ids := sc.sqlIDs()
	from := start.AddDate(0, -23, 0)
	to := start.AddDate(0, 1, 0)
	tz := s.loc.String()

	// 24 months of trends (the screen shows 12; the year before feeds the comparisons).
	rows, err := s.db.QueryContext(ctx, monthsSQL, tenantID, from.Format("2006-01-02"), to.Format("2006-01-02"), ids, tz)
	if err != nil {
		return nil, fmt.Errorf("insights months: %w", err)
	}
	for rows.Next() {
		var m MonthPoint
		if err := rows.Scan(&m.Period, &m.Billed, &m.Collected, &m.WorkOpened, &m.WorkClosed, &m.ContractsSigned, &m.SalesValue); err != nil {
			rows.Close()
			return nil, err
		}
		if m.Billed.IsPositive() {
			r := m.Collected.Div(m.Billed).Mul(decimal.NewFromInt(100)).Round(1)
			m.CollectionRate = &r
		}
		out.Months = append(out.Months, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Position now.
	var avgResolve *float64
	var units, occupied, available, openWO int
	var overdue decimal.Decimal
	if err := s.db.QueryRowContext(ctx, positionSQL, tenantID, ids).Scan(&out.KPIs.Outstanding, &units, &occupied, &available,
		&openWO, &avgResolve, &out.Sales.ContractValue, &out.Sales.ContractCollected, &overdue); err != nil {
		return nil, fmt.Errorf("insights position: %w", err)
	}
	k := &out.KPIs
	k.Units, k.Occupied, k.Available, k.OpenWorkOrders = units, occupied, available, openWO
	if units > 0 {
		p := decimal.NewFromInt(int64(occupied)).Div(decimal.NewFromInt(int64(units))).Mul(decimal.NewFromInt(100)).Round(1)
		k.OccupancyPct = &p
	}
	if avgResolve != nil {
		h := decimal.NewFromFloat(*avgResolve).Round(1)
		k.AvgResolveHours = &h
	}

	// Comparisons: this month, last month, same month last year.
	byPeriod := map[string]MonthPoint{}
	for _, m := range out.Months {
		byPeriod[m.Period] = m
	}
	cur := byPeriod[period]
	prev, hasPrev := byPeriod[start.AddDate(0, -1, 0).Format("2006-01")]
	yr, hasYr := byPeriod[start.AddDate(-1, 0, 0).Format("2006-01")]
	kpi := func(pick func(MonthPoint) *decimal.Decimal) KPI {
		v := KPI{}
		if c := pick(cur); c != nil {
			v.Value = *c
		}
		if hasPrev {
			v.LastMonth = pick(prev)
		}
		if hasYr {
			v.LastYear = pick(yr)
		}
		return v
	}
	k.Billed = kpi(func(m MonthPoint) *decimal.Decimal { b := m.Billed; return &b })
	k.Collected = kpi(func(m MonthPoint) *decimal.Decimal { c := m.Collected; return &c })
	k.CollectionRate = kpi(func(m MonthPoint) *decimal.Decimal { return m.CollectionRate })

	// Days sales outstanding: owed now over average daily billing of the last three months.
	billed3, collected6, billed6 := decimal.Zero, decimal.Zero, decimal.Zero
	for i := 1; i <= 6; i++ {
		m := byPeriod[start.AddDate(0, -i, 0).Format("2006-01")]
		if i <= 3 {
			billed3 = billed3.Add(m.Billed)
		}
		billed6 = billed6.Add(m.Billed)
		collected6 = collected6.Add(m.Collected)
	}
	if billed3.IsPositive() {
		d := k.Outstanding.Div(billed3.Div(decimal.NewFromInt(90))).Round(0)
		k.DaysSalesOutstanding = &d
	}

	// Forecast: known instalments plus recurring charges at the recent collection rate.
	rate := decimal.NewFromInt(1)
	if billed6.IsPositive() {
		rate = decimal.Min(collected6.Div(billed6), decimal.NewFromInt(1))
	}
	avgBilled := billed3.Div(decimal.NewFromInt(3)).Round(2)
	recurring := avgBilled.Mul(rate).Round(2)
	out.ForecastBasis = ForecastBasis{AvgMonthlyBilled: avgBilled, CollectionRate6m: rate.Mul(decimal.NewFromInt(100)).Round(1),
		OverdueInstalments: overdue,
		Method:             "Each month: instalments still due that month, plus the last three months' average billing times the last six months' collection rate."}
	next := start.AddDate(0, 1, 0)
	fcEnd := next.AddDate(0, 12, 0)
	inst := map[string]decimal.Decimal{}
	frows, err := s.db.QueryContext(ctx, instalmentForecastSQL, tenantID, next.Format("2006-01-02"), fcEnd.Format("2006-01-02"), ids, tz)
	if err != nil {
		return nil, fmt.Errorf("insights forecast: %w", err)
	}
	for frows.Next() {
		var p string
		var amt decimal.Decimal
		if err := frows.Scan(&p, &amt); err != nil {
			frows.Close()
			return nil, err
		}
		inst[p] = amt
	}
	frows.Close()
	for i := 0; i < 12; i++ {
		p := next.AddDate(0, i, 0).Format("2006-01")
		row := ForecastRow{Period: p, Instalments: inst[p], Recurring: recurring}
		row.Total = row.Instalments.Add(row.Recurring)
		out.Forecast = append(out.Forecast, row)
	}

	// Sales pace over the last six months.
	for i := 0; i < 6; i++ {
		out.Sales.SignedLast6m += byPeriod[start.AddDate(0, -i, 0).Format("2006-01")].ContractsSigned
	}
	out.Sales.Available = available
	out.Sales.MonthlyRate = decimal.NewFromInt(int64(out.Sales.SignedLast6m)).Div(decimal.NewFromInt(6)).Round(1)
	if out.Sales.MonthlyRate.IsPositive() {
		m := decimal.NewFromInt(int64(available)).Div(out.Sales.MonthlyRate).Round(1)
		out.Sales.MonthsToSellOut = &m
	}

	// Revenue mix over the last three complete months.
	mrows, err := s.db.QueryContext(ctx, mixSQL, tenantID, start.AddDate(0, -3, 0).Format("2006-01"), period, ids)
	if err != nil {
		return nil, fmt.Errorf("insights mix: %w", err)
	}
	for mrows.Next() {
		var r MixRow
		if err := mrows.Scan(&r.ChargeCode, &r.Amount); err != nil {
			mrows.Close()
			return nil, err
		}
		out.RevenueMix = append(out.RevenueMix, r)
	}
	mrows.Close()

	// Blocks compared on last complete month's billing and what they owe now.
	brows, err := s.db.QueryContext(ctx, blocksSQL, tenantID, start.AddDate(0, -1, 0).Format("2006-01"), ids)
	if err != nil {
		return nil, fmt.Errorf("insights blocks: %w", err)
	}
	for brows.Next() {
		var r BlockRow
		if err := brows.Scan(&r.BlockID, &r.Name, &r.Units, &r.Billed, &r.Owing); err != nil {
			brows.Close()
			return nil, err
		}
		out.Blocks = append(out.Blocks, r)
	}
	brows.Close()

	// Maintenance by category.
	wrows, err := s.db.QueryContext(ctx, workSQL, tenantID, ids)
	if err != nil {
		return nil, fmt.Errorf("insights work: %w", err)
	}
	for wrows.Next() {
		var r WorkCategoryRow
		var avg *float64
		if err := wrows.Scan(&r.Category, &r.Opened, &r.Completed, &r.Breached, &avg); err != nil {
			wrows.Close()
			return nil, err
		}
		if avg != nil {
			h := decimal.NewFromFloat(*avg).Round(1)
			r.AvgResolveHours = &h
		}
		out.WorkByType = append(out.WorkByType, r)
	}
	wrows.Close()

	// The screen shows the last 12 months; the earlier 12 only fed the comparisons.
	if len(out.Months) > 12 {
		out.Months = out.Months[len(out.Months)-12:]
	}
	return out, nil
}
