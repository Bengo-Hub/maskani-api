package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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
	"github.com/bengobox/maskani-api/internal/modules/treasury"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
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
	go s.issueLines(tenantguard.With(context.Background(), tenantID), run.ID)
	return run, nil
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
func (s *Service) issueLines(ctx context.Context, runID uuid.UUID) {
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
		for _, l := range lines {
			s.issueOne(ctx, tenantID, run, f, prop, l)
		}
	}
	s.finishRun(ctx, tenantID, runID)
}

func (s *Service) issueOne(ctx context.Context, tenantID uuid.UUID, run *ent.BillingRun, f *ent.Fund, prop *ent.Property, l *ent.BillingRunLine) {
	acc, err := s.client.UnitAccount.Get(ctx, l.UnitAccountID)
	if err != nil {
		_ = l.Update().SetStatus(billingrunline.StatusFailed).AddAttempts(1).SetLastError(err.Error()).Exec(ctx)
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
	for _, raw := range l.Lines {
		b, _ := json.Marshal(raw)
		var ln Line
		_ = json.Unmarshal(b, &ln)
		qty, _ := ln.Quantity.Float64()
		amt, _ := ln.Amount.Float64()
		price := amt
		if qty > 0 && ln.ChargeCode != "" {
			rate, _ := ln.Rate.Float64()
			if rate > 0 && ln.Quantity.Mul(ln.Rate).Round(2).Equal(ln.Amount) {
				price = rate
			} else {
				qty = 1
			}
		} else {
			qty = 1
		}
		req.Lines = append(req.Lines, treasury.InvoiceLine{Description: ln.Description, ItemSKU: ln.ChargeCode,
			ItemType: "service", Quantity: qty, UnitPrice: price, TaxRate: ln.TaxRate})
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
		"name": acc.CustomerName, "paybill": f.PaybillShortcode, "fund": f.Code,
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
	_ = tx.Commit()
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
	go s.issueLines(tenantguard.With(context.Background(), tenantID), runID)
	return run, nil
}

// ListRuns returns the most recent runs, optionally for one property.
func (s *Service) ListRuns(ctx context.Context, propertyID *uuid.UUID, limit int) ([]*ent.BillingRun, error) {
	q := s.client.BillingRun.Query()
	if propertyID != nil {
		q = q.Where(billingrun.PropertyID(*propertyID))
	}
	return q.Order(ent.Desc(billingrun.FieldCreatedAt)).Limit(min(max(limit, 1), 100)).All(ctx)
}

// RunLines returns a run's lines.
func (s *Service) RunLines(ctx context.Context, runID uuid.UUID) ([]*ent.BillingRunLine, error) {
	return s.client.BillingRunLine.Query().Where(billingrunline.RunID(runID)).
		Order(ent.Asc(billingrunline.FieldUnitCode)).All(ctx)
}
