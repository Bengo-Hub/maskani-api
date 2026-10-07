// Package reports serves dashboards and reports from SQL aggregates and the daily_stats table,
// never by loading raw rows into memory (SRDD section 19, NFR-05).
package reports

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"entgo.io/ent/dialect/sql"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/billingrun"
	"github.com/bengobox/maskani-api/internal/ent/billingrunline"
	"github.com/bengobox/maskani-api/internal/ent/dailystat"
	"github.com/bengobox/maskani-api/internal/ent/salecontract"
	"github.com/bengobox/maskani-api/internal/ent/unit"
	"github.com/bengobox/maskani-api/internal/ent/unitaccount"
	"github.com/bengobox/maskani-api/internal/ent/vendorcontract"
	"github.com/bengobox/maskani-api/internal/ent/vendordocument"
	"github.com/bengobox/maskani-api/internal/ent/workorder"
	"github.com/bengobox/maskani-api/internal/modules/utilities"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
	"github.com/bengobox/maskani-api/internal/shared/sqlx"
)

// cacheTTL bounds dashboard staleness (SRDD 17.2: 60 seconds, invalidated by events).
const cacheTTL = 60 * time.Second

// Service is the reports service.
type Service struct {
	client    *ent.Client
	utilities *utilities.Service
	loc       *time.Location
	log       *zap.Logger

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	v  any
	at time.Time
}

// NewService creates the reports service. client should be the read replica client when present.
func NewService(client *ent.Client, util *utilities.Service, loc *time.Location, log *zap.Logger) *Service {
	return &Service{client: client, utilities: util, loc: loc, log: log.Named("reports"), cache: map[string]cacheEntry{}}
}

func (s *Service) cached(ctx context.Context, key string, fn func() (any, error)) (any, error) {
	tenantID, _ := tenantguard.TenantID(ctx)
	k := tenantID.String() + ":" + key
	s.mu.Lock()
	if e, ok := s.cache[k]; ok && time.Since(e.at) < cacheTTL {
		s.mu.Unlock()
		return e.v, nil
	}
	s.mu.Unlock()
	v, err := fn()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.cache[k] = cacheEntry{v: v, at: time.Now()}
	s.mu.Unlock()
	return v, nil
}

// Invalidate drops a tenant's cached figures (called after billing runs and payments).
func (s *Service) Invalidate(tenantID uuid.UUID) {
	prefix := tenantID.String() + ":"
	s.mu.Lock()
	for k := range s.cache {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			delete(s.cache, k)
		}
	}
	s.mu.Unlock()
}

// Dashboard is the manager dashboard (SRDD figure 14).
type Dashboard struct {
	Period            string           `json:"period"`
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
}

// Dashboard computes the dashboard for a property (or all visible properties) and month.
func (s *Service) Dashboard(ctx context.Context, propertyID *uuid.UUID, period string) (*Dashboard, error) {
	if period == "" {
		period = time.Now().In(s.loc).Format("2006-01")
	}
	key := "dashboard:" + period
	if propertyID != nil {
		key += ":" + propertyID.String()
	}
	v, err := s.cached(ctx, key, func() (any, error) { return s.dashboard(ctx, propertyID, period) })
	if err != nil {
		return nil, err
	}
	return v.(*Dashboard), nil
}

func (s *Service) dashboard(ctx context.Context, propertyID *uuid.UUID, period string) (*Dashboard, error) {
	d := &Dashboard{Period: period}
	start, _ := time.ParseInLocation("2006-01", period, s.loc)
	end := start.AddDate(0, 1, 0)

	// Billed: issued lines of this period's runs.
	var billed []struct {
		Total decimal.Decimal `json:"total"`
	}
	lq := s.client.BillingRunLine.Query().Where(billingrunline.StatusEQ(billingrunline.StatusIssued),
		billingrunline.HasRunWith(billingrun.Period(period)))
	if propertyID != nil {
		lq = lq.Where(billingrunline.HasRunWith(billingrun.PropertyID(*propertyID)))
	}
	if err := lq.Aggregate(sqlx.SumAs(billingrunline.FieldTotal, "total", "")).Scan(ctx, &billed); err != nil {
		return nil, err
	}
	if len(billed) > 0 {
		d.Billed = billed[0].Total
	}

	// Collected: daily_stats maintained by the payment consumer.
	var coll []struct {
		Collected decimal.Decimal `json:"collected"`
	}
	sq := s.client.DailyStat.Query().Where(dailystat.DayGTE(start), dailystat.DayLT(end))
	if propertyID != nil {
		sq = sq.Where(dailystat.PropertyID(*propertyID))
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
	if propertyID != nil {
		aq = aq.Where(unitaccount.HasUnitWith(unit.PropertyID(*propertyID)))
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
	if propertyID != nil {
		wq = wq.Where(workorder.PropertyID(*propertyID))
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
	if propertyID != nil {
		uq = uq.Where(unit.PropertyID(*propertyID))
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
	if propertyID != nil {
		cq = cq.Where(salecontract.PropertyID(*propertyID))
	}
	var sc []struct {
		Value decimal.Decimal `json:"value"`
		Paid  decimal.Decimal `json:"paid"`
	}
	if err := cq.Aggregate(sqlx.SumAs(salecontract.FieldNetPrice, "value", ""), sqlx.SumAs(salecontract.FieldPaidTotal, "paid", "")).Scan(ctx, &sc); err != nil {
		return nil, err
	}
	if len(sc) > 0 {
		d.SalesValue, d.SalesCollected = sc[0].Value, sc[0].Paid
	}

	// Vendors due: contracts ending or documents expiring within 30 days.
	soon := time.Now().AddDate(0, 0, 30)
	nc, _ := s.client.VendorContract.Query().Where(vendorcontract.StatusEQ(vendorcontract.StatusActive),
		vendorcontract.EndsOnNotNil(), vendorcontract.EndsOnLT(soon)).Count(ctx)
	nd, _ := s.client.VendorDocument.Query().Where(vendordocument.ExpiresAtNotNil(), vendordocument.ExpiresAtLT(soon)).Count(ctx)
	d.VendorsDue = nc + nd

	if propertyID != nil && s.utilities != nil {
		if pts, err := s.utilities.WaterBalance(ctx, *propertyID, period); err == nil && len(pts) > 0 {
			last := pts[len(pts)-1]
			if last.Supplied.IsPositive() {
				d.WaterLossPct = &last.LossPct
			}
		}
	}
	return d, nil
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

// Arrears lists owing accounts, largest first (bounded).
func (s *Service) Arrears(ctx context.Context, propertyID *uuid.UUID, limit int) ([]ArrearsRow, error) {
	q := s.client.UnitAccount.Query().Where(unitaccount.BalanceGT(decimal.Zero))
	if propertyID != nil {
		q = q.Where(unitaccount.HasUnitWith(unit.PropertyID(*propertyID)))
	}
	rows, err := q.Order(ent.Desc(unitaccount.FieldBalance)).Limit(min(max(limit, 1), 1000)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ArrearsRow, len(rows))
	for i, a := range rows {
		out[i] = ArrearsRow{AccountID: a.ID, AccountRef: a.AccountRef, Customer: a.CustomerName, Phone: a.CustomerPhone,
			Balance: a.Balance, LastPaid: a.LastPaymentAt}
	}
	return out, nil
}

// SalesPosition counts units by sale status and sums contract values.
type SalesPosition struct {
	ByStatus  map[string]int  `json:"by_status"`
	Value     decimal.Decimal `json:"contract_value"`
	Collected decimal.Decimal `json:"collected"`
	Balance   decimal.Decimal `json:"balance"`
}

// Sales returns the sales position for a property.
func (s *Service) Sales(ctx context.Context, propertyID uuid.UUID) (*SalesPosition, error) {
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
