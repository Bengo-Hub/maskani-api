package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	sharedcache "github.com/Bengo-Hub/cache"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/billingrun"
	"github.com/bengobox/maskani-api/internal/ent/billingrunline"
	"github.com/bengobox/maskani-api/internal/ent/fund"
	"github.com/bengobox/maskani-api/internal/ent/meter"
	"github.com/bengobox/maskani-api/internal/ent/meterreading"
	"github.com/bengobox/maskani-api/internal/ent/property"
	"github.com/bengobox/maskani-api/internal/ent/unit"
	"github.com/bengobox/maskani-api/internal/ent/unitaccount"
	"github.com/bengobox/maskani-api/internal/ent/unitcharge"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/accounts"
	"github.com/bengobox/maskani-api/internal/modules/treasury"
	"github.com/bengobox/maskani-api/internal/platform/realtime"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
	"github.com/bengobox/maskani-api/internal/shared/page"
)

// batchSize bounds memory and treasury load per step (SRDD 17.2).
const batchSize = 500

// PreviewLine is one unit's computed bill.
type PreviewLine struct {
	UnitID        uuid.UUID       `json:"unit_id"`
	UnitCode      string          `json:"unit_code"`
	AccountID     *uuid.UUID      `json:"unit_account_id,omitempty"`
	AccountRef    string          `json:"account_ref,omitempty"`
	CustomerName  string          `json:"customer_name,omitempty"`
	CustomerPhone string          `json:"-"`
	PartyID       *uuid.UUID      `json:"party_id,omitempty"`
	Lines         []Line          `json:"lines"`
	Subtotal      decimal.Decimal `json:"subtotal"`
	Tax           decimal.Decimal `json:"tax"`
	Total         decimal.Decimal `json:"total"`
	SkipReason    string          `json:"skip_reason,omitempty"`
}

// Preview is a computed run, not yet issued.
type Preview struct {
	PropertyID uuid.UUID       `json:"property_id"`
	Fund       string          `json:"fund"`
	Period     string          `json:"period"`
	Units      int             `json:"units"`
	Billable   int             `json:"billable"`
	Skipped    int             `json:"skipped"`
	Total      decimal.Decimal `json:"total"`
	Lines      []PreviewLine   `json:"lines"`
}

// Compute builds the bill for every unit of the property in the fund and period.
func (s *Service) Compute(ctx context.Context, propertyID uuid.UUID, fundCode, period string) (*Preview, error) {
	p, err := MonthPeriod(period, s.loc)
	if err != nil {
		return nil, httpx.Invalid("period must be YYYY-MM")
	}
	f, err := s.client.Fund.Query().Where(fund.Code(fundCode)).Only(ctx)
	if err != nil {
		return nil, httpx.Invalid("fund not configured: " + fundCode)
	}
	charges, rates, err := s.loadCatalog(ctx, fundCode)
	if err != nil {
		return nil, err
	}
	units, err := s.client.Unit.Query().
		Where(unit.PropertyID(propertyID), unit.StatusEQ(unit.StatusActive)).
		Order(ent.Asc(unit.FieldWalkingOrder), ent.Asc(unit.FieldCode)).All(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, len(units))
	for i, u := range units {
		ids[i] = u.ID
	}
	accs, err := s.client.UnitAccount.Query().
		Where(unitaccount.FundID(f.ID), unitaccount.UnitIDIn(ids...), unitaccount.StatusEQ(unitaccount.StatusActive)).All(ctx)
	if err != nil {
		return nil, err
	}
	accByUnit := map[uuid.UUID]*ent.UnitAccount{}
	for _, a := range accs {
		accByUnit[a.UnitID] = a
	}
	optIn, err := s.optIns(ctx, ids, p)
	if err != nil {
		return nil, err
	}
	usage, notes, err := s.consumption(ctx, propertyID, period)
	if err != nil {
		return nil, err
	}

	out := &Preview{PropertyID: propertyID, Fund: fundCode, Period: period, Units: len(units)}
	for _, u := range units {
		pl := PreviewLine{UnitID: u.ID, UnitCode: u.Code}
		acc := accByUnit[u.ID]
		if acc == nil {
			pl.SkipReason = "no account in this fund (no owner or buyer linked)"
			out.Skipped++
			out.Lines = append(out.Lines, pl)
			continue
		}
		pl.AccountID, pl.AccountRef = &acc.ID, acc.AccountRef
		pl.CustomerName, pl.CustomerPhone, pl.PartyID = acc.CustomerName, acc.CustomerPhone, acc.PrimaryPartyID
		info := UnitInfo{ID: u.ID, Code: u.Code, PropertyID: u.PropertyID, UnitType: u.UnitType,
			Entitlement: u.Entitlement, OptIn: optIn[u.ID], Consumption: usage[u.ID], ReadingNote: notes[u.ID]}
		if u.SizeSqm != nil {
			info.SizeSqm = *u.SizeSqm
		}
		if f.Kind == fund.KindEstate && u.HandedOverAt != nil {
			info.ActiveFrom = *u.HandedOverAt
		}
		pl.Lines = ComputeUnit(charges, rates, info, p)
		pl.Subtotal, pl.Tax, pl.Total = Totals(pl.Lines)
		if !pl.Total.IsPositive() {
			pl.SkipReason = "nothing to bill"
			out.Skipped++
		} else {
			out.Billable++
			out.Total = out.Total.Add(pl.Total)
		}
		out.Lines = append(out.Lines, pl)
	}
	return out, nil
}

func (s *Service) optIns(ctx context.Context, unitIDs []uuid.UUID, p Period) (map[uuid.UUID]map[string]decimal.Decimal, error) {
	rows, err := s.client.UnitCharge.Query().
		Where(unitcharge.UnitIDIn(unitIDs...), unitcharge.StatusEQ(unitcharge.StatusActive),
			unitcharge.StartDateLT(p.End), unitcharge.Or(unitcharge.EndDateIsNil(), unitcharge.EndDateGT(p.Start))).
		All(ctx)
	if err != nil {
		return nil, err
	}
	codes := map[uuid.UUID]string{}
	out := map[uuid.UUID]map[string]decimal.Decimal{}
	for _, r := range rows {
		code, ok := codes[r.ChargeTypeID]
		if !ok {
			ct, err := s.client.ChargeType.Get(ctx, r.ChargeTypeID)
			if err != nil {
				continue
			}
			code, codes[r.ChargeTypeID] = ct.Code, ct.Code
		}
		if out[r.UnitID] == nil {
			out[r.UnitID] = map[string]decimal.Decimal{}
		}
		q := r.Quantity
		if !q.IsPositive() {
			q = decimal.NewFromInt(1)
		}
		out[r.UnitID][code] = out[r.UnitID][code].Add(q)
	}
	return out, nil
}

// consumption sums the period's non-rejected readings on unit water meters, per unit.
func (s *Service) consumption(ctx context.Context, propertyID uuid.UUID, period string) (map[uuid.UUID]map[string]decimal.Decimal, map[uuid.UUID]map[string]string, error) {
	meters, err := s.client.Meter.Query().
		Where(meter.PropertyID(propertyID), meter.KindEQ(meter.KindUnit), meter.UnitIDNotNil()).All(ctx)
	if err != nil || len(meters) == 0 {
		return map[uuid.UUID]map[string]decimal.Decimal{}, map[uuid.UUID]map[string]string{}, err
	}
	meterIDs := make([]uuid.UUID, len(meters))
	chargeOf := map[uuid.UUID]string{}
	for i, m := range meters {
		meterIDs[i] = m.ID
		code := "water"
		if c, ok := m.Metadata["charge_code"].(string); ok && c != "" {
			code = c
		}
		chargeOf[m.ID] = code
	}
	readings, err := s.client.MeterReading.Query().
		Where(meterreading.MeterIDIn(meterIDs...), meterreading.Period(period),
			meterreading.StatusNEQ(meterreading.StatusRejected),
			meterreading.SourceIn(meterreading.SourceRound, meterreading.SourceEstimate, meterreading.SourceImport,
				meterreading.SourceReplacement)).All(ctx)
	if err != nil {
		return nil, nil, err
	}
	usage := map[uuid.UUID]map[string]decimal.Decimal{}
	notes := map[uuid.UUID]map[string]string{}
	for _, r := range readings {
		if r.UnitID == nil {
			continue
		}
		code := chargeOf[r.MeterID]
		if usage[*r.UnitID] == nil {
			usage[*r.UnitID], notes[*r.UnitID] = map[string]decimal.Decimal{}, map[string]string{}
		}
		usage[*r.UnitID][code] = usage[*r.UnitID][code].Add(r.Consumption)
		prev := "0"
		if r.PreviousReading != nil {
			prev = r.PreviousReading.StringFixed(0)
		}
		note := fmt.Sprintf("reading %s less %s = %s m3", r.Reading.StringFixed(0), prev, r.Consumption.String())
		if r.IsEstimated {
			note += ", estimated"
		}
		notes[*r.UnitID][code] = note
	}
	return usage, notes, nil
}

// IssueInput starts a run.
type IssueInput struct {
	PropertyID  uuid.UUID  `json:"property_id"`
	Fund        string     `json:"fund"`
	Period      string     `json:"period"`
	InvoiceDate *time.Time `json:"invoice_date"`
	DueDate     *time.Time `json:"due_date"`
}

// Issue creates the run and its lines in one transaction, then issues invoices in the background.
// A second request for the same property, fund and period returns the existing run.
func (s *Service) Issue(ctx context.Context, actor uuid.UUID, in IssueInput) (*ent.BillingRun, error) {
	if in.Fund == "" {
		in.Fund = "estate"
	}
	f, err := s.client.Fund.Query().Where(fund.Code(in.Fund)).Only(ctx)
	if err != nil {
		return nil, httpx.Invalid("fund not configured: " + in.Fund)
	}
	if existing, err := s.client.BillingRun.Query().Where(billingrun.PropertyID(in.PropertyID), billingrun.FundID(f.ID),
		billingrun.Period(in.Period), billingrun.RunKindEQ(billingrun.RunKindRegular),
		billingrun.StatusNEQ(billingrun.StatusCancelled)).Only(ctx); err == nil {
		return existing, nil
	}
	pv, err := s.Compute(ctx, in.PropertyID, in.Fund, in.Period)
	if err != nil {
		return nil, err
	}
	invoiceDate, dueDate, err := s.dates(ctx, in)
	if err != nil {
		return nil, err
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	run, err := tx.BillingRun.Create().SetPropertyID(in.PropertyID).SetFundID(f.ID).SetPeriod(in.Period).
		SetInvoiceDate(invoiceDate).SetDueDate(dueDate).SetUnitCount(pv.Units).SetLineCount(pv.Billable).
		SetTotalAmount(pv.Total).SetSkippedCount(pv.Skipped).SetStartedBy(actor).
		SetStatus(billingrun.StatusIssuing).Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsConstraintError(err) {
			return nil, httpx.Conflict("a run for this period is already in progress")
		}
		return nil, err
	}
	creates := make([]*ent.BillingRunLineCreate, 0, len(pv.Lines))
	for _, l := range pv.Lines {
		if l.AccountID == nil || l.SkipReason != "" {
			continue
		}
		raw, _ := json.Marshal(l.Lines)
		var lines []map[string]any
		_ = json.Unmarshal(raw, &lines)
		c := tx.BillingRunLine.Create().SetRunID(run.ID).SetUnitID(l.UnitID).SetUnitAccountID(*l.AccountID).
			SetUnitCode(l.UnitCode).SetLines(lines).SetSubtotal(l.Subtotal).SetTaxTotal(l.Tax).SetTotal(l.Total)
		if l.PartyID != nil {
			c.SetPartyID(*l.PartyID)
		}
		creates = append(creates, c)
	}
	for i := 0; i < len(creates); i += batchSize {
		end := min(i+batchSize, len(creates))
		if err := tx.BillingRunLine.CreateBulk(creates[i:end]...).Exec(ctx); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	s.progress(tenantID, run)
	go s.issueLines(tenantguard.With(context.Background(), tenantID), run.ID)
	return run, nil
}

// stuckAfter is how long a run may sit in "issuing" without a batch finishing before the resume
// job takes it over (a batch of 500 S2S calls takes well under this).
const stuckAfter = 5 * time.Minute

// ResumeStuck restarts runs left in "issuing" by a pod that stopped (system job, all tenants).
// Lines already issued are skipped and treasury's by-reference check stops a double invoice, so
// resuming is safe; the per-run lease stops two pods issuing the same run.
func (s *Service) ResumeStuck(ctx context.Context) (int, error) {
	runs, err := s.client.BillingRun.Query().Where(billingrun.StatusEQ(billingrun.StatusIssuing),
		billingrun.UpdatedAtLT(time.Now().Add(-stuckAfter))).Select(billingrun.FieldID, billingrun.FieldTenantID).
		Limit(50).All(ctx)
	if err != nil {
		return 0, err
	}
	for _, r := range runs {
		go s.issueLines(tenantguard.With(context.Background(), r.TenantID), r.ID)
	}
	return len(runs), nil
}

// progress publishes a run progress hint. Called once per batch, never per line.
func (s *Service) progress(tenantID uuid.UUID, run *ent.BillingRun) {
	if run == nil {
		return
	}
	realtime.Emit(s.rt, tenantID, realtime.Event{Type: realtime.BillingRunProgress, ID: run.ID.String(),
		PropertyID: run.PropertyID.String()})
}

func (s *Service) dates(ctx context.Context, in IssueInput) (time.Time, time.Time, error) {
	p, err := MonthPeriod(in.Period, s.loc)
	if err != nil {
		return time.Time{}, time.Time{}, httpx.Invalid("period must be YYYY-MM")
	}
	billingDay, dueDay := 1, 10
	if st, err := s.client.TenantSetting.Query().First(ctx); err == nil {
		billingDay, dueDay = st.BillingDay, st.DueDay
	}
	inv := p.Start.AddDate(0, 0, billingDay-1)
	due := p.Start.AddDate(0, 0, dueDay-1)
	if in.InvoiceDate != nil {
		inv = *in.InvoiceDate
	}
	if in.DueDate != nil {
		due = *in.DueDate
	}
	if due.Before(inv) {
		return time.Time{}, time.Time{}, httpx.Invalid("due date is before the invoice date")
	}
	return inv, due, nil
}

// issueLines raises invoices for pending and failed lines in batches, then settles the run status.
// It holds a fleet-wide lease on the run while it works (renewed as it goes), so a resume never
// runs alongside a live issue on another pod.
func (s *Service) issueLines(ctx context.Context, runID uuid.UUID) {
	if s.rdb == nil {
		s.issueLinesLocked(ctx, runID)
		return
	}
	ran, err := sharedcache.RunExclusive(ctx, s.rdb, s.log, "maskani:billing-run:"+runID.String(), 2*time.Minute,
		func(lctx context.Context) error {
			s.issueLinesLocked(lctx, runID)
			return nil
		})
	if err != nil {
		s.log.Warn("billing run lease unavailable; the resume job will retry", zap.String("run", runID.String()), zap.Error(err))
	} else if !ran {
		s.log.Info("billing run already issuing on another pod", zap.String("run", runID.String()))
	}
}

func (s *Service) issueLinesLocked(ctx context.Context, runID uuid.UUID) {
	run, err := s.client.BillingRun.Get(ctx, runID)
	if err != nil {
		s.log.Error("billing run not found", zap.Error(err))
		return
	}
	f, _ := s.client.Fund.Get(ctx, run.FundID)
	prop, _ := s.client.Property.Query().Where(property.ID(run.PropertyID)).Only(ctx)
	tenantID, _ := tenantguard.TenantID(ctx)
	for {
		lines, err := s.client.BillingRunLine.Query().
			Where(billingrunline.RunID(runID), billingrunline.StatusIn(billingrunline.StatusPending, billingrunline.StatusFailed),
				billingrunline.AttemptsLT(3)).
			Order(ent.Asc(billingrunline.FieldUnitCode)).Limit(batchSize).All(ctx)
		if err != nil || len(lines) == 0 {
			break
		}
		// The batch's accounts in one read instead of one per line.
		accIDs := make([]uuid.UUID, len(lines))
		for i, l := range lines {
			accIDs[i] = l.UnitAccountID
		}
		accs := map[uuid.UUID]*ent.UnitAccount{}
		if rows, err := s.client.UnitAccount.Query().Where(unitaccount.IDIn(accIDs...)).All(ctx); err == nil {
			for _, a := range rows {
				accs[a.ID] = a
			}
		}
		for _, l := range lines {
			if ctx.Err() != nil {
				return // lease lost or shutting down; the resume job carries on
			}
			s.issueOne(ctx, tenantID, run, f, prop, l, accs[l.UnitAccountID])
		}
		// Each finished batch marks the run alive, so the resume job leaves it alone.
		_ = s.client.BillingRun.UpdateOneID(runID).SetUpdatedAt(time.Now()).Exec(ctx)
		s.progress(tenantID, run)
	}
	s.finishRun(ctx, tenantID, runID)
}

func (s *Service) issueOne(ctx context.Context, tenantID uuid.UUID, run *ent.BillingRun, f *ent.Fund, prop *ent.Property,
	l *ent.BillingRunLine, acc *ent.UnitAccount) {
	if acc == nil {
		_ = l.Update().SetStatus(billingrunline.StatusFailed).AddAttempts(1).SetLastError("unit account not found").Exec(ctx)
		return
	}
	req := treasury.CreateInvoiceRequest{
		CustomerName: acc.CustomerName, CustomerPhone: acc.CustomerPhone, InvoiceType: "standard",
		InvoiceDate: run.InvoiceDate, DueDate: run.DueDate, Currency: "KES",
		ReferenceID: &l.ID, ReferenceType: treasury.RefBill,
		Notes: fmt.Sprintf("%s %s. Pay to paybill %s, account %s.", l.UnitCode, run.Period, f.PaybillShortcode, acc.AccountRef),
		Metadata: map[string]any{"account_ref": acc.AccountRef, "unit_account_id": acc.ID.String(), "fund": f.Code,
			"period": run.Period, "unit_code": l.UnitCode, "billing_run_id": run.ID.String(), "source_service": treasury.SourceService},
	}
	if prop != nil && prop.OutletID != nil {
		req.OutletID = prop.OutletID
	}
	if f.TreasuryBankAccountID != nil {
		req.SettlementAccountID = f.TreasuryBankAccountID
	}
	// items is the bill breakdown carried on bill.issued, so the email and WhatsApp messages show
	// each charge, not only the total.
	items := make([]map[string]any, 0, len(l.Lines))
	for _, raw := range l.Lines {
		b, _ := json.Marshal(raw)
		var ln Line
		_ = json.Unmarshal(b, &ln)
		qty, _ := ln.Quantity.Float64()
		amt, _ := ln.Amount.Float64()
		price := amt
		rated := false
		if qty > 0 && ln.ChargeCode != "" {
			rate, _ := ln.Rate.Float64()
			if rate > 0 && ln.Quantity.Mul(ln.Rate).Round(2).Equal(ln.Amount) {
				price, rated = rate, true
			} else {
				qty = 1
			}
		} else {
			qty = 1
		}
		req.Lines = append(req.Lines, treasury.InvoiceLine{Description: ln.Description, ItemSKU: ln.ChargeCode,
			ItemType: "service", Quantity: qty, UnitPrice: price, TaxRate: ln.TaxRate})
		item := map[string]any{"description": ln.Description, "amount": ln.Amount.StringFixed(2)}
		// Quantity and rate only where they explain the amount (metered water, per-sqm charges).
		if rated && !ln.Quantity.Equal(decimal.NewFromInt(1)) {
			item["quantity"], item["rate"] = ln.Quantity.String(), ln.Rate.StringFixed(2)
		}
		if ln.Tax.IsPositive() {
			item["tax"] = ln.Tax.StringFixed(2)
		}
		items = append(items, item)
	}
	inv, err := s.treasury.IssueInvoice(ctx, tenantID, req)
	if err != nil {
		s.log.Warn("invoice failed", zap.String("unit", l.UnitCode), zap.Error(err))
		_ = l.Update().SetStatus(billingrunline.StatusFailed).AddAttempts(1).SetLastError(err.Error()).Exec(ctx)
		return
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return
	}
	if err := tx.BillingRunLine.UpdateOneID(l.ID).SetStatus(billingrunline.StatusIssued).SetTreasuryInvoiceID(inv.ID).
		SetInvoiceNumber(inv.InvoiceNumber).AddAttempts(1).ClearLastError().Exec(ctx); err != nil {
		_ = tx.Rollback()
		return
	}
	_ = events.Publish(ctx, tx.OutboxEvent, tenantID, l.ID.String(), events.BillIssued, map[string]any{
		"unit_code": l.UnitCode, "account_ref": acc.AccountRef, "amount": l.Total.StringFixed(2),
		"due_date": run.DueDate.Format("2 Jan 2006"), "period": run.Period, "invoice_id": inv.ID,
		"invoice_number": inv.InvoiceNumber, "pay_token": inv.PublicToken, "phone": acc.CustomerPhone,
		"email": accounts.CustomerEmail(acc),
		"name":  acc.CustomerName, "paybill": f.PaybillShortcode, "fund": f.Code, "fund_name": f.Name,
		"items": items, "subtotal": l.Subtotal.StringFixed(2), "tax_total": l.TaxTotal.StringFixed(2),
		"invoice_date": run.InvoiceDate.Format("2 Jan 2006"),
	})
	_ = tx.Commit()
}

func (s *Service) finishRun(ctx context.Context, tenantID, runID uuid.UUID) {
	var counts []struct {
		Status string `json:"status"`
		Count  int    `json:"count"`
	}
	_ = s.client.BillingRunLine.Query().Where(billingrunline.RunID(runID)).
		GroupBy(billingrunline.FieldStatus).Aggregate(ent.Count()).Scan(ctx, &counts)
	issued, failed := 0, 0
	for _, c := range counts {
		switch c.Status {
		case "issued":
			issued = c.Count
		case "failed", "pending":
			failed += c.Count
		}
	}
	status := billingrun.StatusIssued
	if failed > 0 {
		status = billingrun.StatusPartiallyFailed
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return
	}
	run, err := tx.BillingRun.UpdateOneID(runID).SetStatus(status).SetIssuedCount(issued).SetFailedCount(failed).
		SetIssuedAt(time.Now()).Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return
	}
	_ = events.Publish(ctx, tx.OutboxEvent, tenantID, runID.String(), events.BillingRunCompleted, map[string]any{
		"run_id": runID, "property_id": run.PropertyID, "period": run.Period, "issued": issued, "failed": failed,
		"total": run.TotalAmount.StringFixed(2),
	})
	if tx.Commit() == nil {
		s.progress(tenantID, run)
	}
}

// Retry re-issues failed lines of a run.
func (s *Service) Retry(ctx context.Context, runID uuid.UUID) (*ent.BillingRun, error) {
	run, err := s.client.BillingRun.Get(ctx, runID)
	if err != nil {
		return nil, err
	}
	if _, err := s.client.BillingRunLine.Update().
		Where(billingrunline.RunID(runID), billingrunline.StatusEQ(billingrunline.StatusFailed)).
		SetAttempts(0).Save(ctx); err != nil {
		return nil, err
	}
	run, err = run.Update().SetStatus(billingrun.StatusIssuing).Save(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	s.progress(tenantID, run)
	go s.issueLines(tenantguard.With(context.Background(), tenantID), runID)
	return run, nil
}

// ListRuns returns a keyset page of runs, newest first, for one property or the caller's scope.
func (s *Service) ListRuns(ctx context.Context, propertyID *uuid.UUID, scope []uuid.UUID, all bool, p page.Params) (page.Result[*ent.BillingRun], error) {
	q := s.client.BillingRun.Query()
	if propertyID != nil {
		q = q.Where(billingrun.PropertyID(*propertyID))
	} else if !all {
		q = q.Where(billingrun.PropertyIDIn(scope...))
	}
	rows, err := q.Where(p.Predicate()).Modify(page.Order()).Limit(p.Limit + 1).All(ctx)
	if err != nil {
		return page.Result[*ent.BillingRun]{}, err
	}
	return page.Build(rows, p.Limit, func(r *ent.BillingRun) (uuid.UUID, time.Time) { return r.ID, r.CreatedAt }), nil
}

// RunPropertyID returns a run's property (scope checks).
func (s *Service) RunPropertyID(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	r, err := s.client.BillingRun.Query().Where(billingrun.ID(id)).Select(billingrun.FieldPropertyID).Only(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	return r.PropertyID, nil
}

// RunCounts are a run's lines by status.
type RunCounts struct {
	Pending int `json:"pending"`
	Issued  int `json:"issued"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
	Total   int `json:"total"`
}

// RunView is a run with its live line counts.
type RunView struct {
	*ent.BillingRun
	Counts RunCounts `json:"counts"`
}

// GetRun returns one run with its line counts by status (one grouped query).
func (s *Service) GetRun(ctx context.Context, id uuid.UUID) (*RunView, error) {
	run, err := s.client.BillingRun.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Status string `json:"status"`
		Count  int    `json:"count"`
	}
	if err := s.client.BillingRunLine.Query().Where(billingrunline.RunID(id)).
		GroupBy(billingrunline.FieldStatus).Aggregate(ent.Count()).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	v := &RunView{BillingRun: run}
	for _, r := range rows {
		switch r.Status {
		case "pending":
			v.Counts.Pending = r.Count
		case "issued":
			v.Counts.Issued = r.Count
		case "failed":
			v.Counts.Failed = r.Count
		case "skipped":
			v.Counts.Skipped = r.Count
		}
		v.Counts.Total += r.Count
	}
	return v, nil
}

// RunLines returns a run's lines.
func (s *Service) RunLines(ctx context.Context, runID uuid.UUID) ([]*ent.BillingRunLine, error) {
	return s.client.BillingRunLine.Query().Where(billingrunline.RunID(runID)).
		Order(ent.Asc(billingrunline.FieldUnitCode)).All(ctx)
}
