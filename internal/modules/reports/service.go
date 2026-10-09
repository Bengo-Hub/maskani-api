// Package reports serves dashboards and reports from SQL aggregates and the daily_stats table,
// never by loading raw rows into memory (SRDD section 19, NFR-05).
package reports

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	sharedcache "github.com/Bengo-Hub/cache"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"entgo.io/ent/dialect/sql"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/billingrun"
	"github.com/bengobox/maskani-api/internal/ent/billingrunline"
	"github.com/bengobox/maskani-api/internal/ent/dailystat"
	"github.com/bengobox/maskani-api/internal/ent/fund"
	"github.com/bengobox/maskani-api/internal/ent/salecontract"
	"github.com/bengobox/maskani-api/internal/ent/unit"
	"github.com/bengobox/maskani-api/internal/ent/unitaccount"
	"github.com/bengobox/maskani-api/internal/ent/vendorcontract"
	"github.com/bengobox/maskani-api/internal/ent/vendordocument"
	"github.com/bengobox/maskani-api/internal/ent/workorder"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/utilities"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
	"github.com/bengobox/maskani-api/internal/shared/page"
	"github.com/bengobox/maskani-api/internal/shared/sqlx"
)

// cacheTTL bounds dashboard staleness (SRDD 17.2: 60 seconds, invalidated by events).
const cacheTTL = 60 * time.Second

// Service is the reports service.
type Service struct {
	client    *ent.Client
	db        *stdsql.DB // read-only handle for the grouped report SQL; may equal the primary
	utilities *utilities.Service
	loc       *time.Location
	log       *zap.Logger

	cache *sharedcache.Local[string, any]
	// gen is a per-tenant generation folded into every cache key: Invalidate bumps it, so a
	// tenant's old entries become unreachable at once and age out of the bounded LRU.
	genMu sync.Mutex
	gen   map[uuid.UUID]uint64
}

// NewService creates the reports service. client and db should be the read replica when present.
func NewService(client *ent.Client, db *stdsql.DB, util *utilities.Service, loc *time.Location, log *zap.Logger) *Service {
	return &Service{client: client, db: db, utilities: util, loc: loc, log: log.Named("reports"),
		cache: sharedcache.NewLocal[string, any](2000, cacheTTL), gen: map[uuid.UUID]uint64{}}
}

func (s *Service) generation(tenantID uuid.UUID) uint64 {
	s.genMu.Lock()
	defer s.genMu.Unlock()
	return s.gen[tenantID]
}

func (s *Service) cached(ctx context.Context, key string, fn func() (any, error)) (any, error) {
	tenantID, _ := tenantguard.TenantID(ctx)
	k := fmt.Sprintf("%s:%d:%s", tenantID, s.generation(tenantID), key)
	return s.cache.GetOrLoad(k, fn)
}

// Invalidate drops a tenant's cached figures. Wired to payment.applied and billing run progress
// on every pod through the realtime relay, and called directly after local writes.
func (s *Service) Invalidate(tenantID uuid.UUID) {
	s.genMu.Lock()
	s.gen[tenantID]++
	s.genMu.Unlock()
}

// Scope limits a report to one property or to the caller's properties.
type Scope struct {
	PropertyID *uuid.UUID
	IDs        []uuid.UUID
	All        bool
}

// ids returns the property filter, or nil when the report covers the whole tenant.
func (sc Scope) ids() []uuid.UUID {
	if sc.PropertyID != nil {
		return []uuid.UUID{*sc.PropertyID}
	}
	if sc.All {
		return nil
	}
	if sc.IDs == nil {
		return []uuid.UUID{}
	}
	return sc.IDs
}

// key is a stable cache key part for the scope.
func (sc Scope) key() string {
	ids := sc.ids()
	if ids == nil {
		return "all"
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = id.String()
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// sqlIDs is the uuid[] parameter for raw SQL: nil means no property filter.
func (sc Scope) sqlIDs() any {
	ids := sc.ids()
	if ids == nil {
		return nil
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return "{" + strings.Join(out, ",") + "}"
}

// Dashboard is the manager dashboard (SRDD figure 14).
type Dashboard struct {
	Period string `json:"period"` // the last month of the range
	From   string `json:"from"`
	To     string `json:"to"`
	// CollectionsScope is "filtered", or "property" when a block or fund is chosen: collections are
	// recorded per property and day, so they follow the property and months only.
	CollectionsScope  string           `json:"collections_scope"`
	Billed            decimal.Decimal  `json:"billed"`
	Collected         decimal.Decimal  `json:"collected"`
	CollectionRate    decimal.Decimal  `json:"collection_rate"`
	Outstanding       decimal.Decimal  `json:"outstanding"`
	AccountsOwing     int              `json:"accounts_owing"`
	Arrears60Accounts int              `json:"arrears_60_accounts"`
	Arrears60Amount   decimal.Decimal  `json:"arrears_60_amount"`
	OpenWorkOrders    int              `json:"open_work_orders"`
	PastSLA           int              `json:"past_sla"`
	WaterLossPct      *decimal.Decimal `json:"water_loss_pct,omitempty"`
	VendorsDue        int              `json:"vendors_due_for_renewal"`
	Units             int              `json:"units"`
	Occupied          int              `json:"occupied"`
	UnitsSold         int              `json:"units_sold"`
	SalesValue        decimal.Decimal  `json:"sales_value"`
	SalesCollected    decimal.Decimal  `json:"sales_collected"`
	// CollectionsByWeek is billed and collected per week (weeks start Monday) for the period.
	CollectionsByWeek []WeekFigures `json:"collections_by_week"`
	// ArrearsAgeing buckets owing accounts by the age of their oldest unpaid due date, allocating
	// each balance to the newest bills first (treasury settles oldest first).
	ArrearsAgeing []AgeBucket `json:"arrears_ageing"`
}

// WeekFigures is one week of the collections chart.
type WeekFigures struct {
	WeekStart string          `json:"week_start"`
	Billed    decimal.Decimal `json:"billed"`
	Collected decimal.Decimal `json:"collected"`
}

// AgeBucket is one arrears ageing band.
type AgeBucket struct {
	Bucket   string          `json:"bucket"`
	Accounts int             `json:"accounts"`
	Amount   decimal.Decimal `json:"amount"`
}

// AgeBuckets are the ageing bands in display order.
var AgeBuckets = []string{"0-30", "31-60", "61-90", "90+"}

// Dashboard computes the dashboard for a property (or the caller's properties) and month. Results
// are cached for 60 seconds per tenant, scope and period.
// DashboardFilter narrows the dashboard: a range of months (From to To, YYYY-MM, at most 12), a
// block and a fund. Block and fund narrow what is kept per unit or account (billed, outstanding,
// arrears, units, work orders, sales); collections are recorded per property, so they follow the
// property and months only and the response says so in CollectionsScope.
type DashboardFilter struct {
	From, To string
	BlockID  *uuid.UUID
	FundID   *uuid.UUID
}

func (f DashboardFilter) key() string {
	k := f.From + ".." + f.To
	if f.BlockID != nil {
		k += ":b" + f.BlockID.String()
	}
	if f.FundID != nil {
		k += ":f" + f.FundID.String()
	}
	return k
}

// unitsOfBlock limits rows with a unit_id column to the units of a block.
func unitsOfBlock(block uuid.UUID) func(*sql.Selector) {
	return func(sel *sql.Selector) {
		ut := sql.Table(unit.Table)
		sel.Where(sql.In(sel.C("unit_id"), sql.Select(ut.C(unit.FieldID)).From(ut).Where(sql.EQ(ut.C(unit.FieldBlockID), block))))
	}
}

func (s *Service) Dashboard(ctx context.Context, sc Scope, f DashboardFilter) (*Dashboard, error) {
	now := time.Now().In(s.loc).Format("2006-01")
	if f.To == "" {
		f.To = now
	}
	if f.From == "" {
		f.From = f.To
	}
	from, err1 := time.ParseInLocation("2006-01", f.From, s.loc)
	to, err2 := time.ParseInLocation("2006-01", f.To, s.loc)
	if err1 != nil || err2 != nil {
		return nil, httpx.Invalid("from and to must be YYYY-MM")
	}
	if to.Before(from) {
		return nil, httpx.Invalid("from is after to")
	}
	if from.AddDate(1, 0, 0).Before(to.AddDate(0, 1, 0)) {
		return nil, httpx.Invalid("choose at most 12 months")
	}
	v, err := s.cached(ctx, "dashboard:"+f.key()+":"+sc.key(), func() (any, error) { return s.dashboard(ctx, sc, f) })
	if err != nil {
		return nil, err
	}
	return v.(*Dashboard), nil
}

func (s *Service) dashboard(ctx context.Context, sc Scope, f DashboardFilter) (*Dashboard, error) {
	period := f.To
	d := &Dashboard{Period: period, From: f.From, To: f.To, CollectionsScope: "filtered"}
	if f.BlockID != nil || f.FundID != nil {
		d.CollectionsScope = "property"
	}
	start, _ := time.ParseInLocation("2006-01", f.From, s.loc)
	last, _ := time.ParseInLocation("2006-01", f.To, s.loc)
	end := last.AddDate(0, 1, 0)
	ids := sc.ids()

	// Billed: issued lines of the months' runs.
	var billed []struct {
		Total decimal.Decimal `json:"total"`
	}
	lq := s.client.BillingRunLine.Query().Where(billingrunline.StatusEQ(billingrunline.StatusIssued),
		billingrunline.HasRunWith(billingrun.PeriodGTE(f.From), billingrun.PeriodLTE(f.To)))
	if ids != nil {
		lq = lq.Where(billingrunline.HasRunWith(billingrun.PropertyIDIn(ids...)))
	}
	if f.FundID != nil {
		lq = lq.Where(billingrunline.HasRunWith(billingrun.FundID(*f.FundID)))
	}
	if f.BlockID != nil {
		lq = lq.Where(unitsOfBlock(*f.BlockID))
	}
	if err := lq.Aggregate(sqlx.SumAs(billingrunline.FieldTotal, "total", "")).Scan(ctx, &billed); err != nil {
		return nil, err
	}
	if len(billed) > 0 {
		d.Billed = billed[0].Total
	}

	// Collected: daily_stats maintained by the payment consumer (per property and day).
	var coll []struct {
		Collected decimal.Decimal `json:"collected"`
	}
	// daily_stats.day holds the local date at UTC midnight (RecordCollection).
	dayFrom := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC)
	dayTo := time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, time.UTC)
	sq := s.client.DailyStat.Query().Where(dailystat.DayGTE(dayFrom), dailystat.DayLT(dayTo))
	if ids != nil {
		sq = sq.Where(dailystat.PropertyIDIn(ids...))
	}
	if err := sq.Aggregate(sqlx.SumAs(dailystat.FieldCollected, "collected", "")).Scan(ctx, &coll); err != nil {
		return nil, err
	}
	if len(coll) > 0 {
		d.Collected = coll[0].Collected
	}
	if d.Billed.IsPositive() {
		d.CollectionRate = d.Collected.Div(d.Billed).Mul(decimal.NewFromInt(100)).Round(1)
	}

	// Outstanding and arrears from cached treasury balances. "Over 60 days" = owing accounts whose
	// oldest issued bill fell due more than 60 days ago.
	aq := s.client.UnitAccount.Query().Where(unitaccount.BalanceGT(decimal.Zero))
	if ids != nil {
		aq = aq.Where(unitaccount.HasUnitWith(unit.PropertyIDIn(ids...)))
	}
	if f.BlockID != nil {
		aq = aq.Where(unitaccount.HasUnitWith(unit.BlockID(*f.BlockID)))
	}
	if f.FundID != nil {
		aq = aq.Where(unitaccount.FundID(*f.FundID))
	}
	var owing []struct {
		N   int             `json:"n"`
		Sum decimal.Decimal `json:"sum"`
	}
	if err := aq.Clone().Aggregate(sqlx.CountAs("n", ""), sqlx.SumAs(unitaccount.FieldBalance, "sum", "")).Scan(ctx, &owing); err != nil {
		return nil, err
	}
	if len(owing) > 0 {
		d.AccountsOwing, d.Outstanding = owing[0].N, owing[0].Sum
	}
	cutoff := time.Now().AddDate(0, 0, -60)
	var old []struct {
		N   int             `json:"n"`
		Sum decimal.Decimal `json:"sum"`
	}
	if err := aq.Clone().Where(func(sel *sql.Selector) {
		lt := sql.Table(billingrunline.Table)
		rt := sql.Table(billingrun.Table)
		sub := sql.Select(lt.C(billingrunline.FieldUnitAccountID)).From(lt).
			Join(rt).On(lt.C(billingrunline.FieldRunID), rt.C(billingrun.FieldID)).
			Where(sql.And(sql.EQ(lt.C(billingrunline.FieldStatus), "issued"), sql.LT(rt.C(billingrun.FieldDueDate), cutoff)))
		sel.Where(sql.In(sel.C(unitaccount.FieldID), sub))
	}).Aggregate(sqlx.CountAs("n", ""), sqlx.SumAs(unitaccount.FieldBalance, "sum", "")).Scan(ctx, &old); err != nil {
		return nil, err
	}
	if len(old) > 0 {
		d.Arrears60Accounts, d.Arrears60Amount = old[0].N, old[0].Sum
	}

	// Work orders.
	wq := s.client.WorkOrder.Query().Where(workorder.StatusNotIn(workorder.StatusConfirmed, workorder.StatusClosed,
		workorder.StatusCancelled, workorder.StatusCompleted))
	if ids != nil {
		wq = wq.Where(workorder.PropertyIDIn(ids...))
	}
	if f.BlockID != nil {
		wq = wq.Where(unitsOfBlock(*f.BlockID))
	}
	var wo []struct {
		Open int `json:"open"`
		Past int `json:"past"`
	}
	if err := wq.Aggregate(sqlx.CountAs("open", ""), sqlx.CountAs("past", "resolution_due_at < now()")).Scan(ctx, &wo); err != nil {
		return nil, err
	}
	if len(wo) > 0 {
		d.OpenWorkOrders, d.PastSLA = wo[0].Open, wo[0].Past
	}

	// Units and sales.
	uq := s.client.Unit.Query().Where(unit.StatusEQ(unit.StatusActive))
	if ids != nil {
		uq = uq.Where(unit.PropertyIDIn(ids...))
	}
	if f.BlockID != nil {
		uq = uq.Where(unit.BlockID(*f.BlockID))
	}
	var uc []struct {
		Units    int `json:"units"`
		Occupied int `json:"occupied"`
		Sold     int `json:"sold"`
	}
	if err := uq.Aggregate(sqlx.CountAs("units", ""), sqlx.CountAs("occupied", "occupancy_status IN ('owner_occupied','tenanted')"),
		sqlx.CountAs("sold", "sale_status IN ('under_agreement','fully_paid','handed_over','titled','in_default')")).Scan(ctx, &uc); err != nil {
		return nil, err
	}
	if len(uc) > 0 {
		d.Units, d.Occupied, d.UnitsSold = uc[0].Units, uc[0].Occupied, uc[0].Sold
	}
	cq := s.client.SaleContract.Query().Where(salecontract.StatusNotIn(salecontract.StatusDraft, salecontract.StatusCancelled, salecontract.StatusTerminated))
	if ids != nil {
		cq = cq.Where(salecontract.PropertyIDIn(ids...))
	}
	if f.BlockID != nil {
		cq = cq.Where(unitsOfBlock(*f.BlockID))
	}
	var sales []struct {
		Value decimal.Decimal `json:"value"`
		Paid  decimal.Decimal `json:"paid"`
	}
	if err := cq.Aggregate(sqlx.SumAs(salecontract.FieldNetPrice, "value", ""), sqlx.SumAs(salecontract.FieldPaidTotal, "paid", "")).Scan(ctx, &sales); err != nil {
		return nil, err
	}
	if len(sales) > 0 {
		d.SalesValue, d.SalesCollected = sales[0].Value, sales[0].Paid
	}

	tenantID, _ := tenantguard.TenantID(ctx)
	var err error
	if d.CollectionsByWeek, err = s.weekly(ctx, tenantID, sc, f, start, end); err != nil {
		return nil, err
	}
	if d.ArrearsAgeing, err = s.ageing(ctx, tenantID, sc, f.BlockID, f.FundID); err != nil {
		return nil, err
	}

	// Vendors due: contracts ending or documents expiring within 30 days.
	soon := time.Now().AddDate(0, 0, 30)
	nc, _ := s.client.VendorContract.Query().Where(vendorcontract.StatusEQ(vendorcontract.StatusActive),
		vendorcontract.EndsOnNotNil(), vendorcontract.EndsOnLT(soon)).Count(ctx)
	nd, _ := s.client.VendorDocument.Query().Where(vendordocument.ExpiresAtNotNil(), vendordocument.ExpiresAtLT(soon)).Count(ctx)
	d.VendorsDue = nc + nd

	if sc.PropertyID != nil && s.utilities != nil {
		if pts, err := s.utilities.WaterBalance(ctx, *sc.PropertyID, period); err == nil && len(pts) > 0 {
			last := pts[len(pts)-1]
			if last.Supplied.IsPositive() {
				d.WaterLossPct = &last.LossPct
			}
		}
	}
	return d, nil
}

// weeklySQL returns billed and collected per week (Monday start) over a range of months, in one grouped query.
// Parameters: $1 tenant, $2 first day (date), $3 first day after the range (date), $4 first period (YYYY-MM),
// $5 property ids as a uuid array literal or NULL, $6 time zone, $7 last period, $8 block or NULL,
// $9 fund or NULL (block and fund narrow billed only). The guard does not apply to raw
// SQL, so every table is filtered by tenant_id here.
const weeklySQL = `
WITH weeks AS (
  SELECT gs::date AS week_start
    FROM generate_series(date_trunc('week', $2::date), ($3::date - 1)::timestamp, interval '1 week') AS gs
), billed AS (
  SELECT date_trunc('week', (r.invoice_date AT TIME ZONE $6))::date AS wk, SUM(l.total) AS amt
    FROM billing_run_lines l JOIN billing_runs r ON r.id = l.run_id AND r.tenant_id = $1
   WHERE l.tenant_id = $1 AND l.status = 'issued' AND r.period >= $4 AND r.period <= $7
     AND ($8::uuid IS NULL OR l.unit_id IN (SELECT id FROM units WHERE tenant_id = $1 AND block_id = $8::uuid))
     AND ($9::uuid IS NULL OR r.fund_id = $9::uuid)
     AND ($5::text IS NULL OR r.property_id = ANY($5::text::uuid[]))
   GROUP BY 1
), coll AS (
  SELECT date_trunc('week', d.day)::date AS wk, SUM(d.collected) AS amt
    FROM daily_stats d
   WHERE d.tenant_id = $1 AND d.day >= $2::date AND d.day < $3::date
     AND ($5::text IS NULL OR d.property_id = ANY($5::text::uuid[]))
   GROUP BY 1
)
SELECT to_char(w.week_start, 'YYYY-MM-DD'), COALESCE(b.amt, 0), COALESCE(c.amt, 0)
  FROM weeks w LEFT JOIN billed b ON b.wk = w.week_start LEFT JOIN coll c ON c.wk = w.week_start
 ORDER BY w.week_start`

// ageingSQL buckets owing accounts by the age of their oldest unpaid due date. Each balance is laid
// against the account's issued bills and instalment invoices from the newest back (treasury settles
// oldest first, so what is still owed is the newest debt); the oldest bill the balance reaches
// sets the age. Accounts with a balance but no issued bill (opening balances) count as 0-30.
// Parameters: $1 tenant, $2 property ids as a uuid array literal or NULL, $3 block or NULL, $4 fund or NULL.
const ageingSQL = `
WITH acc AS (
  SELECT ua.id, ua.balance
    FROM unit_accounts ua JOIN units u ON u.id = ua.unit_id AND u.tenant_id = $1
   WHERE ua.tenant_id = $1 AND ua.status = 'active' AND ua.balance > 0
     AND ($2::text IS NULL OR u.property_id = ANY($2::text::uuid[]))
     AND ($3::uuid IS NULL OR u.block_id = $3::uuid)
     AND ($4::uuid IS NULL OR ua.fund_id = $4::uuid)
), dues AS (
  SELECT l.unit_account_id AS acc_id, r.due_date, l.total AS amount
    FROM billing_run_lines l JOIN billing_runs r ON r.id = l.run_id AND r.tenant_id = $1
   WHERE l.tenant_id = $1 AND l.status = 'issued' AND l.unit_account_id IN (SELECT id FROM acc)
  UNION ALL
  SELECT sc.unit_account_id, i.due_date, i.amount
    FROM instalments i JOIN sale_contracts sc ON sc.id = i.contract_id AND sc.tenant_id = $1
   WHERE i.tenant_id = $1 AND i.treasury_invoice_id IS NOT NULL AND sc.unit_account_id IN (SELECT id FROM acc)
), running AS (
  SELECT acc_id, due_date, amount,
         SUM(amount) OVER (PARTITION BY acc_id ORDER BY due_date DESC ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS upto
    FROM dues
), oldest AS (
  SELECT r.acc_id, MIN(r.due_date) AS oldest_due
    FROM running r JOIN acc a ON a.id = r.acc_id
   WHERE r.upto - r.amount < a.balance
   GROUP BY r.acc_id
), aged AS (
  SELECT a.balance, GREATEST(0, CURRENT_DATE - COALESCE(o.oldest_due::date, CURRENT_DATE)) AS age
    FROM acc a LEFT JOIN oldest o ON o.acc_id = a.id
)
SELECT CASE WHEN age <= 30 THEN '0-30' WHEN age <= 60 THEN '31-60' WHEN age <= 90 THEN '61-90' ELSE '90+' END,
       COUNT(*), COALESCE(SUM(balance), 0)
  FROM aged GROUP BY 1`

func (s *Service) weekly(ctx context.Context, tenantID uuid.UUID, sc Scope, f DashboardFilter, start, end time.Time) ([]WeekFigures, error) {
	out := []WeekFigures{}
	if s.db == nil {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, weeklySQL, tenantID, start.Format("2006-01-02"), end.Format("2006-01-02"),
		f.From, sc.sqlIDs(), s.loc.String(), f.To, nullUUID(f.BlockID), nullUUID(f.FundID))
	if err != nil {
		return nil, fmt.Errorf("collections by week: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var w WeekFigures
		if err := rows.Scan(&w.WeekStart, &w.Billed, &w.Collected); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Service) ageing(ctx context.Context, tenantID uuid.UUID, sc Scope, block, fund *uuid.UUID) ([]AgeBucket, error) {
	byName := map[string]AgeBucket{}
	if s.db != nil {
		rows, err := s.db.QueryContext(ctx, ageingSQL, tenantID, sc.sqlIDs(), nullUUID(block), nullUUID(fund))
		if err != nil {
			return nil, fmt.Errorf("arrears ageing: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var b AgeBucket
			if err := rows.Scan(&b.Bucket, &b.Accounts, &b.Amount); err != nil {
				return nil, err
			}
			byName[b.Bucket] = b
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	out := make([]AgeBucket, len(AgeBuckets))
	for i, name := range AgeBuckets {
		b, ok := byName[name]
		if !ok {
			b = AgeBucket{Bucket: name}
		}
		out[i] = b
	}
	return out, nil
}

// ArrearsRow is one owing account.
type ArrearsRow struct {
	AccountID  uuid.UUID       `json:"account_id"`
	AccountRef string          `json:"account_ref"`
	Customer   string          `json:"customer_name"`
	Phone      string          `json:"customer_phone"`
	Balance    decimal.Decimal `json:"balance"`
	LastPaid   *time.Time      `json:"last_payment_at,omitempty"`
}

// Arrears returns a keyset page of owing accounts, largest balance first, for one property or the
// caller's properties. Pages are cached for 60 seconds like the dashboard.
func (s *Service) Arrears(ctx context.Context, propertyID *uuid.UUID, scope []uuid.UUID, all bool, f ArrearsFilter, p page.DecimalParams) (page.Result[ArrearsRow], error) {
	sc := Scope{PropertyID: propertyID, IDs: scope, All: all}
	key := fmt.Sprintf("arrears:%s:%s:%s:%d:%t:%s:%s", sc.key(), f.Q, f.Min, p.Limit, p.HasAfter, p.AfterID, p.AfterVal)
	v, err := s.cached(ctx, key, func() (any, error) { return s.arrears(ctx, propertyID, scope, all, f, p) })
	if err != nil {
		return page.Result[ArrearsRow]{}, err
	}
	return v.(page.Result[ArrearsRow]), nil
}

// ExportRowLimit caps a downloaded list; a longer one is cut and the document says so.
const ExportRowLimit = 10000

// ArrearsAll returns every owing account matching the filter, largest balance first, walking the
// same keyset as the screen a page at a time (not cached: a download is a deliberate one-off).
// The bool is true when the list was cut at ExportRowLimit.
func (s *Service) ArrearsAll(ctx context.Context, sc Scope, f ArrearsFilter) ([]ArrearsRow, bool, error) {
	var out []ArrearsRow
	p := page.DecimalParams{Limit: page.MaxLimit}
	for {
		res, err := s.arrears(ctx, sc.PropertyID, sc.IDs, sc.All, f, p)
		if err != nil {
			return nil, false, err
		}
		out = append(out, res.Data...)
		if !res.HasMore {
			return out, false, nil
		}
		if len(out) >= ExportRowLimit {
			return out[:ExportRowLimit], true, nil
		}
		last := res.Data[len(res.Data)-1]
		p = page.DecimalParams{Limit: page.MaxLimit, AfterID: last.AccountID, AfterVal: last.Balance, HasAfter: true}
	}
}

// Ageing is the owing balance by days since due for a scope, cached 60 seconds.
func (s *Service) Ageing(ctx context.Context, sc Scope) ([]AgeBucket, error) {
	v, err := s.cached(ctx, "ageing:"+sc.key(), func() (any, error) {
		tenantID, _ := tenantguard.TenantID(ctx)
		return s.ageing(ctx, tenantID, sc, nil, nil)
	})
	if err != nil {
		return nil, err
	}
	return v.([]AgeBucket), nil
}

// WaterBalance is the property's water balance trend, read from the replica and cached.
func (s *Service) WaterBalance(ctx context.Context, propertyID uuid.UUID, period string) ([]utilities.BalancePoint, error) {
	v, err := s.cached(ctx, "water:"+propertyID.String()+":"+period, func() (any, error) {
		return s.utilities.WaterBalance(ctx, propertyID, period)
	})
	if err != nil {
		return nil, err
	}
	return v.([]utilities.BalancePoint), nil
}

// ArrearsFilter narrows the arrears list in SQL, so a search finds accounts beyond the loaded page.
type ArrearsFilter struct {
	// Q matches the start of the account reference (B07, S-B07) or any part of the customer name.
	Q string
	// Min keeps balances at or above this amount.
	Min decimal.Decimal
}

func (s *Service) arrears(ctx context.Context, propertyID *uuid.UUID, scope []uuid.UUID, all bool, f ArrearsFilter, p page.DecimalParams) (page.Result[ArrearsRow], error) {
	q := s.client.UnitAccount.Query().Where(unitaccount.BalanceGT(decimal.Zero), unitaccount.StatusEQ(unitaccount.StatusActive))
	if f.Min.IsPositive() {
		q = q.Where(unitaccount.BalanceGTE(f.Min))
	}
	if t := strings.TrimSpace(f.Q); t != "" {
		q = q.Where(unitaccount.Or(unitaccount.AccountRefHasPrefix(strings.ToUpper(t)), unitaccount.CustomerNameContainsFold(t)))
	}
	if propertyID != nil {
		q = q.Where(unitaccount.HasUnitWith(unit.PropertyID(*propertyID)))
	} else if !all {
		q = q.Where(unitaccount.HasUnitWith(unit.PropertyIDIn(scope...)))
	}
	rows, err := q.Where(p.Predicate(unitaccount.FieldBalance)).Modify(page.OrderDecimal(unitaccount.FieldBalance)).
		Limit(p.Limit + 1).All(ctx)
	if err != nil {
		return page.Result[ArrearsRow]{}, err
	}
	out := make([]ArrearsRow, len(rows))
	for i, a := range rows {
		out[i] = ArrearsRow{AccountID: a.ID, AccountRef: a.AccountRef, Customer: a.CustomerName, Phone: a.CustomerPhone,
			Balance: a.Balance, LastPaid: a.LastPaymentAt}
	}
	return page.BuildDecimal(out, p.Limit, func(r ArrearsRow) (uuid.UUID, decimal.Decimal) { return r.AccountID, r.Balance }), nil
}

// SalesPosition counts units by sale status and sums contract values.
type SalesPosition struct {
	ByStatus  map[string]int  `json:"by_status"`
	Value     decimal.Decimal `json:"contract_value"`
	Collected decimal.Decimal `json:"collected"`
	Balance   decimal.Decimal `json:"balance"`
}

// Sales returns the sales position for a property (cached 60 seconds).
func (s *Service) Sales(ctx context.Context, propertyID uuid.UUID) (*SalesPosition, error) {
	v, err := s.cached(ctx, "sales:"+propertyID.String(), func() (any, error) { return s.sales(ctx, propertyID) })
	if err != nil {
		return nil, err
	}
	return v.(*SalesPosition), nil
}

func (s *Service) sales(ctx context.Context, propertyID uuid.UUID) (*SalesPosition, error) {
	var rows []struct {
		SaleStatus string `json:"sale_status"`
		Count      int    `json:"count"`
	}
	if err := s.client.Unit.Query().Where(unit.PropertyID(propertyID), unit.StatusEQ(unit.StatusActive)).
		GroupBy(unit.FieldSaleStatus).Aggregate(ent.Count()).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := &SalesPosition{ByStatus: map[string]int{}}
	for _, r := range rows {
		out.ByStatus[r.SaleStatus] = r.Count
	}
	var sc []struct {
		Value decimal.Decimal `json:"value"`
		Paid  decimal.Decimal `json:"paid"`
	}
	if err := s.client.SaleContract.Query().Where(salecontract.PropertyID(propertyID),
		salecontract.StatusNotIn(salecontract.StatusDraft, salecontract.StatusCancelled, salecontract.StatusTerminated)).
		Aggregate(sqlx.SumAs(salecontract.FieldNetPrice, "value", ""), sqlx.SumAs(salecontract.FieldPaidTotal, "paid", "")).
		Scan(ctx, &sc); err != nil {
		return nil, err
	}
	if len(sc) > 0 {
		out.Value, out.Collected, out.Balance = sc[0].Value, sc[0].Paid, sc[0].Value.Sub(sc[0].Paid)
	}
	return out, nil
}

// RecordCollection adds a payment to the property's daily figure (payment consumer).
func RecordCollection(ctx context.Context, client *ent.Client, tenantID, propertyID uuid.UUID, at time.Time, amount decimal.Decimal, loc *time.Location) error {
	day := time.Date(at.In(loc).Year(), at.In(loc).Month(), at.In(loc).Day(), 0, 0, 0, 0, time.UTC)
	return client.DailyStat.Create().SetTenantID(tenantID).SetPropertyID(propertyID).SetDay(day).
		SetCollected(amount).SetPaymentsCount(1).
		OnConflictColumns(dailystat.FieldTenantID, dailystat.FieldPropertyID, dailystat.FieldDay).
		Update(func(u *ent.DailyStatUpsert) {
			u.AddCollected(amount)
			u.AddPaymentsCount(1)
		}).Exec(ctx)
}

// nullUUID is a nullable uuid parameter for raw SQL.
func nullUUID(id *uuid.UUID) any {
	if id == nil {
		return nil
	}
	return id.String()
}

// FilterIDs resolves a dashboard's block and fund: the block's property (for the scope check)
// and the fund's id from its code. Unknown values are refused.
func (s *Service) FilterIDs(ctx context.Context, blockID *uuid.UUID, fundCode string) (blockProperty *uuid.UUID, fundID *uuid.UUID, err error) {
	if blockID != nil {
		b, err := s.client.Block.Get(ctx, *blockID)
		if err != nil {
			return nil, nil, httpx.Invalid("unknown block")
		}
		blockProperty = &b.PropertyID
	}
	if fundCode != "" {
		f, err := s.client.Fund.Query().Where(fund.Code(fundCode)).Only(ctx)
		if err != nil {
			return nil, nil, httpx.Invalid("unknown fund")
		}
		fundID = &f.ID
	}
	return blockProperty, fundID, nil
}
