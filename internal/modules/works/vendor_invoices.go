package works

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/approvalrequest"
	"github.com/bengobox/maskani-api/internal/ent/servicevisit"
	"github.com/bengobox/maskani-api/internal/ent/vendor"
	"github.com/bengobox/maskani-api/internal/ent/vendorcontract"
	"github.com/bengobox/maskani-api/internal/ent/vendorinvoice"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/approvals"
	"github.com/bengobox/maskani-api/internal/modules/rbac"
	"github.com/bengobox/maskani-api/internal/modules/treasury"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// Vendor invoices (wave 2.8). An invoice is recorded exactly as the vendor issued it, with its KRA
// eTIMS number, because treasury's bill must match the supplier's own eTIMS sale (KRA matches a
// purchase by supplier PIN and total). The service credit for missed visits is therefore never
// netted into the bill: the comparison shows what the contract and the visits say is due, and a
// manager approves an invoice that already reflects any credit or sends it back for a corrected one.
//
// Approval runs on the central engine (module vendor_bill: one approval by someone who manages
// vendors, never the person who entered it, unless an estate's rules say more). Approved, the
// invoice publishes maskani.vendor_invoice.approved in the same transaction and treasury raises the
// bill through its own approval path, so eTIMS queues the purchase as for any bill. Cancelling an
// approved invoice publishes maskani.vendor_invoice.cancelled and treasury voids its bill unless KRA
// may already hold it. The paid date comes from treasury (SyncInvoicePayments).

const (
	invoiceModule = "vendor_bill"
	// VendorInvoiceApproved and VendorInvoiceCancelled are treasury's triggers.
	VendorInvoiceApproved  = "vendor_invoice.approved"
	VendorInvoiceCancelled = "vendor_invoice.cancelled"
)

// InvoiceInput is a vendor invoice as entered from the paper or PDF the vendor sent.
type InvoiceInput struct {
	ContractID    uuid.UUID       `json:"contract_id"`
	InvoiceNumber string          `json:"invoice_number"`
	EtimsNumber   string          `json:"etims_number"`
	InvoiceDate   time.Time       `json:"invoice_date"`
	PeriodStart   time.Time       `json:"period_start"`
	PeriodEnd     time.Time       `json:"period_end"`
	Amount        decimal.Decimal `json:"amount"`     // total as invoiced, VAT included
	VATAmount     decimal.Decimal `json:"vat_amount"` // the VAT in that total
	Attachments   []string        `json:"attachments"`
}

func (in *InvoiceInput) check() error {
	in.InvoiceNumber, in.EtimsNumber = strings.TrimSpace(in.InvoiceNumber), strings.TrimSpace(in.EtimsNumber)
	switch {
	case in.ContractID == uuid.Nil:
		return httpx.Invalid("choose the contract the invoice is for")
	case in.InvoiceNumber == "":
		return httpx.Invalid("enter the vendor's invoice number")
	case in.InvoiceDate.IsZero() || in.PeriodStart.IsZero() || in.PeriodEnd.IsZero():
		return httpx.Invalid("enter the invoice date and the period it covers")
	case in.PeriodEnd.Before(in.PeriodStart):
		return httpx.Invalid("the period ends before it starts")
	case in.PeriodEnd.Sub(in.PeriodStart) > 370*24*time.Hour:
		return httpx.Invalid("an invoice covers at most a year")
	case !in.Amount.IsPositive():
		return httpx.Invalid("the invoice total must be above zero")
	case in.VATAmount.IsNegative() || in.VATAmount.GreaterThanOrEqual(in.Amount):
		return httpx.Invalid("the VAT must be less than the invoice total")
	case len(in.Attachments) > 5:
		return httpx.Invalid("attach at most 5 files")
	}
	return nil
}

// InvoiceProperty returns an invoice's property (for the handler's scope check).
func (s *Service) InvoiceProperty(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	v, err := s.client.VendorInvoice.Query().Where(vendorinvoice.ID(id)).Select(vendorinvoice.FieldPropertyID).Only(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	return v.PropertyID, nil
}

// CreateInvoice records an invoice under a live contract and sends it for approval. The vendor must
// be linked to its treasury supplier first, so approval can always raise the bill. The same invoice
// number twice for one vendor is refused.
func (s *Service) CreateInvoice(ctx context.Context, in InvoiceInput, by uuid.UUID, byName string) (*ent.VendorInvoice, error) {
	if err := in.check(); err != nil {
		return nil, err
	}
	c, err := s.client.VendorContract.Query().Where(vendorcontract.ID(in.ContractID)).Only(ctx)
	if err != nil {
		return nil, err
	}
	if c.Status != vendorcontract.StatusActive && c.Status != vendorcontract.StatusExpired {
		return nil, httpx.Conflict("invoices are recorded against an active or ended contract, not a draft or terminated one")
	}
	v, err := s.client.Vendor.Query().Where(vendor.ID(c.VendorID)).Only(ctx)
	if err != nil {
		return nil, err
	}
	if v.TreasuryVendorID == nil {
		return nil, httpx.Conflict("link " + v.Name + " to its treasury supplier first, so the approved invoice can be paid")
	}
	if dup, _ := s.client.VendorInvoice.Query().Where(vendorinvoice.VendorID(v.ID), vendorinvoice.InvoiceNumber(in.InvoiceNumber)).Exist(ctx); dup {
		return nil, httpx.Conflict("invoice " + in.InvoiceNumber + " from " + v.Name + " is already recorded")
	}
	inv, err := s.client.VendorInvoice.Create().SetVendorID(v.ID).SetContractID(c.ID).SetPropertyID(c.PropertyID).
		SetInvoiceNumber(in.InvoiceNumber).SetEtimsNumber(in.EtimsNumber).SetInvoiceDate(in.InvoiceDate).
		SetPeriodStart(in.PeriodStart).SetPeriodEnd(in.PeriodEnd).SetAmount(in.Amount.Round(2)).SetVatAmount(in.VATAmount.Round(2)).
		SetAttachments(in.Attachments).SetMetadata(map[string]any{"entered_by": by.String(), "entered_by_name": byName}).Save(ctx)
	if ent.IsConstraintError(err) {
		return nil, httpx.Conflict("invoice " + in.InvoiceNumber + " from " + v.Name + " is already recorded")
	}
	if err != nil {
		return nil, err
	}
	if s.approvals == nil {
		return inv, nil
	}
	pid := c.PropertyID
	_, required, err := s.approvals.Submit(ctx, approvals.Submission{Module: invoiceModule, ObjectID: inv.ID, Reference: in.InvoiceNumber,
		Amount: inv.Amount, PropertyID: &pid, By: by, ByName: byName,
		Meta: map[string]any{"label": "Invoice " + in.InvoiceNumber + " from " + v.Name, "vendor_id": v.ID.String()}})
	if err != nil {
		return inv, err
	}
	if !required {
		if err := s.applyInvoice(ctx, inv.ID, true, by, "No approval needed for this amount"); err != nil {
			return inv, err
		}
	}
	return s.client.VendorInvoice.Get(ctx, inv.ID)
}

// setInvoiceApprovals puts invoice approval on the central engine (called from SetApprovals).
func (s *Service) setInvoiceApprovals(ap *approvals.Service) {
	ap.SetDefault(invoiceModule, approvals.Step{Sequence: 1, Name: "Approve the vendor invoice", Permission: rbac.PermVendorsManage})
	ap.OnDecision(invoiceModule, func(ctx context.Context, req *ent.ApprovalRequest) error {
		var last approvals.Action
		for _, a := range approvals.Actions(req) {
			if a.Status == "approved" || a.Status == "rejected" {
				last = a
			}
		}
		by := uuid.Nil
		if last.ActedBy != nil {
			by = *last.ActedBy
		}
		return s.applyInvoice(ctx, req.ObjectID, req.Status == approvalrequest.StatusApproved, by, last.Comment)
	})
}

// DecideInvoice approves or rejects the current step of an invoice's approval, the same as deciding
// it in the central inbox. A rejection needs a note (what the vendor must correct).
func (s *Service) DecideInvoice(ctx context.Context, id uuid.UUID, by approvals.Actor, d approvals.Decision, note string) (*ent.VendorInvoice, error) {
	if s.approvals == nil {
		return nil, httpx.Unavailable("approvals are not set up")
	}
	if d == approvals.Reject && strings.TrimSpace(note) == "" {
		return nil, httpx.Invalid("say what the vendor must correct")
	}
	req, err := s.approvals.Latest(ctx, id)
	if err != nil {
		return nil, err
	}
	if req == nil || req.Status != approvalrequest.StatusPending {
		return nil, httpx.Conflict("this invoice is not waiting for approval")
	}
	if _, err := s.approvals.Decide(ctx, req.ID, by, d, note); err != nil {
		return nil, err
	}
	return s.client.VendorInvoice.Get(ctx, id)
}

// applyInvoice records the decision once (a submitted invoice only) and, when approved, publishes
// the event treasury raises the bill from, in the same transaction.
func (s *Service) applyInvoice(ctx context.Context, id uuid.UUID, approved bool, by uuid.UUID, note string) error {
	inv, err := s.client.VendorInvoice.Get(ctx, id)
	if err != nil {
		return err
	}
	if inv.Status != vendorinvoice.StatusSubmitted {
		return nil // already decided
	}
	to := vendorinvoice.StatusRejected
	if approved {
		to = vendorinvoice.StatusApproved
	}
	meta := map[string]any{}
	maps.Copy(meta, inv.Metadata)
	meta["decided_by"] = by.String()
	if note = strings.TrimSpace(note); note != "" {
		meta["decision_note"] = note
	}
	var payload map[string]any
	if approved {
		if payload, err = s.invoicePayload(ctx, inv); err != nil {
			return err
		}
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	n, err := tx.VendorInvoice.Update().Where(vendorinvoice.ID(id), vendorinvoice.StatusEQ(vendorinvoice.StatusSubmitted)).
		SetStatus(to).SetDecidedAt(time.Now()).SetMetadata(meta).Save(ctx)
	if err != nil || n == 0 {
		_ = tx.Rollback()
		return err
	}
	if approved {
		tenantID, _ := tenantguard.TenantID(ctx)
		if err := events.Publish(ctx, tx.OutboxEvent, tenantID, id.String(), VendorInvoiceApproved, payload); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// invoicePayload is what treasury needs to raise the bill: the supplier, the invoice as issued and
// where the cost belongs. Never a copy of anything treasury owns.
func (s *Service) invoicePayload(ctx context.Context, inv *ent.VendorInvoice) (map[string]any, error) {
	v, err := s.client.Vendor.Get(ctx, inv.VendorID)
	if err != nil {
		return nil, err
	}
	if v.TreasuryVendorID == nil {
		return nil, httpx.Conflict("link " + v.Name + " to its treasury supplier before approving its invoice")
	}
	c, err := s.client.VendorContract.Get(ctx, inv.ContractID)
	if err != nil {
		return nil, err
	}
	period := inv.PeriodStart.Format("2 Jan") + " to " + inv.PeriodEnd.Format("2 Jan 2006")
	return map[string]any{
		"invoice_id":         inv.ID.String(),
		"treasury_vendor_id": v.TreasuryVendorID.String(),
		"vendor_name":        v.Name,
		"invoice_number":     inv.InvoiceNumber,
		"etims_number":       inv.EtimsNumber,
		"invoice_date":       inv.InvoiceDate.Format(time.RFC3339),
		"amount":             inv.Amount.StringFixed(2),
		"vat_amount":         inv.VatAmount.StringFixed(2),
		"currency":           "KES",
		"description":        fmt.Sprintf("%s, %s (invoice %s)", strings.ReplaceAll(c.ServiceCategory, "_", " "), period, inv.InvoiceNumber),
		"property_id":        inv.PropertyID.String(),
		"contract_id":        c.ID.String(),
		"budget_line":        c.BudgetLineCode,
	}, nil
}

// CancelInvoice withdraws an invoice that is not paid: a submitted one leaves the approvals queue;
// an approved one also tells treasury to void its bill (refused there once KRA may hold it).
func (s *Service) CancelInvoice(ctx context.Context, id, by uuid.UUID, reason string) (*ent.VendorInvoice, error) {
	if reason = strings.TrimSpace(reason); reason == "" {
		return nil, httpx.Invalid("say why the invoice is cancelled")
	}
	inv, err := s.client.VendorInvoice.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if inv.Status != vendorinvoice.StatusSubmitted && inv.Status != vendorinvoice.StatusApproved {
		return nil, httpx.Conflict("only an invoice waiting for approval or approved and not yet paid can be cancelled")
	}
	meta := map[string]any{}
	maps.Copy(meta, inv.Metadata)
	meta["cancelled_by"], meta["cancel_reason"] = by.String(), reason
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	n, err := tx.VendorInvoice.Update().Where(vendorinvoice.ID(id), vendorinvoice.StatusEQ(inv.Status)).
		SetStatus(vendorinvoice.StatusCancelled).SetMetadata(meta).Save(ctx)
	if err != nil || n == 0 {
		_ = tx.Rollback()
		if err == nil {
			err = httpx.Conflict("the invoice changed meanwhile; reload it")
		}
		return nil, err
	}
	if inv.Status == vendorinvoice.StatusApproved {
		tenantID, _ := tenantguard.TenantID(ctx)
		if err := events.Publish(ctx, tx.OutboxEvent, tenantID, id.String(), VendorInvoiceCancelled,
			map[string]any{"invoice_id": id.String(), "reason": reason}); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if inv.Status == vendorinvoice.StatusSubmitted && s.approvals != nil {
		_ = s.approvals.Cancel(ctx, id)
	}
	return s.client.VendorInvoice.Get(ctx, id)
}

// InvoiceFilter narrows the invoice list.
type InvoiceFilter struct {
	PropertyID *uuid.UUID
	VendorID   *uuid.UUID
	Status     string
	Scope      []uuid.UUID
	All        bool
}

// Invoices lists invoices newest first, at most 200 (an estate has a few vendors a month).
func (s *Service) Invoices(ctx context.Context, f InvoiceFilter) ([]*ent.VendorInvoice, error) {
	q := s.client.VendorInvoice.Query()
	switch {
	case f.PropertyID != nil:
		q = q.Where(vendorinvoice.PropertyID(*f.PropertyID))
	case !f.All:
		q = q.Where(vendorinvoice.PropertyIDIn(f.Scope...))
	}
	if f.VendorID != nil {
		q = q.Where(vendorinvoice.VendorID(*f.VendorID))
	}
	if f.Status != "" {
		if vendorinvoice.StatusValidator(vendorinvoice.Status(f.Status)) != nil {
			return nil, httpx.Invalid("unknown invoice status")
		}
		q = q.Where(vendorinvoice.StatusEQ(vendorinvoice.Status(f.Status)))
	}
	return q.Order(ent.Desc(vendorinvoice.FieldInvoiceDate), ent.Desc(vendorinvoice.FieldCreatedAt)).Limit(200).All(ctx)
}

// InvoiceCheck sets an invoice beside what its contract and the visits say is due.
type InvoiceCheck struct {
	FeeBasis       string          `json:"fee_basis"`
	FeeAmount      decimal.Decimal `json:"fee_amount"`
	Months         int             `json:"months"`
	Visits         map[string]int  `json:"visits"` // scheduled, completed, partial, missed
	CreditPercent  decimal.Decimal `json:"credit_percent"`
	CreditDue      decimal.Decimal `json:"credit_due"`
	ExpectedAmount decimal.Decimal `json:"expected_amount"` // the contract amount less the credit, when Computed
	Computed       bool            `json:"computed"`        // false for per post and rate card fees
	Invoiced       decimal.Decimal `json:"invoiced"`
	Difference     decimal.Decimal `json:"difference"` // invoiced less expected, when Computed
}

// CheckInvoice compares an invoice with its contract: the fee for the period, the visits planned,
// done and missed in it (one grouped count) and the service credit for missed visits. A fixed
// monthly fee counts the calendar months the period touches; a per visit fee counts visits done or
// part done; per post and rate card fees are shown without a computed amount.
func (s *Service) CheckInvoice(ctx context.Context, id uuid.UUID) (*InvoiceCheck, error) {
	inv, err := s.client.VendorInvoice.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	c, err := s.client.VendorContract.Get(ctx, inv.ContractID)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Status string `json:"status"`
		Count  int    `json:"count"`
	}
	if err := s.client.ServiceVisit.Query().Where(servicevisit.ContractID(c.ID), servicevisit.DueAtGTE(inv.PeriodStart),
		servicevisit.DueAtLT(inv.PeriodEnd.AddDate(0, 0, 1))).GroupBy(servicevisit.FieldStatus).Aggregate(ent.Count()).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := &InvoiceCheck{FeeBasis: string(c.FeeBasis), FeeAmount: c.FeeAmount, Visits: map[string]int{}, Invoiced: inv.Amount}
	for _, r := range rows {
		out.Visits[r.Status] = r.Count
	}
	for _, sc := range c.ServiceCredits {
		if b, _ := sc["breach"].(string); b == "missed_visit" {
			if p, ok := sc["percent"].(float64); ok {
				out.CreditPercent = decimal.NewFromFloat(p)
			}
		}
	}
	out.Months = monthsTouched(inv.PeriodStart, inv.PeriodEnd)
	missed := decimal.NewFromInt(int64(out.Visits["missed"]))
	pct := out.CreditPercent.Div(decimal.NewFromInt(100))
	switch c.FeeBasis {
	case vendorcontract.FeeBasisFixedMonthly:
		gross := c.FeeAmount.Mul(decimal.NewFromInt(int64(out.Months)))
		out.CreditDue = decimal.Min(gross, c.FeeAmount.Mul(pct).Mul(missed)).Round(2)
		out.ExpectedAmount, out.Computed = gross.Sub(out.CreditDue), true
	case vendorcontract.FeeBasisPerVisit:
		done := decimal.NewFromInt(int64(out.Visits["completed"] + out.Visits["partial"]))
		gross := c.FeeAmount.Mul(done)
		out.CreditDue = decimal.Min(gross, c.FeeAmount.Mul(pct).Mul(missed)).Round(2)
		out.ExpectedAmount, out.Computed = gross.Sub(out.CreditDue), true
	default:
		out.CreditDue = c.FeeAmount.Mul(pct).Mul(missed).Round(2)
	}
	if out.Computed {
		out.Difference = inv.Amount.Sub(out.ExpectedAmount)
	}
	return out, nil
}

// SyncInvoicePayments asks treasury about the bills it raised for approved invoices (system job,
// at most 1,000 invoices a run, 200 per treasury call) and records each bill's number and status;
// an invoice whose bill is paid is marked paid on the date treasury last changed the bill.
func (s *Service) SyncInvoicePayments(ctx context.Context, tenants []uuid.UUID) (int, error) {
	if len(tenants) == 0 || s.treasury == nil || !s.treasury.Enabled() {
		return 0, nil
	}
	rows, err := s.client.VendorInvoice.Query().Where(vendorinvoice.TenantIDIn(tenants...), vendorinvoice.StatusEQ(vendorinvoice.StatusApproved)).
		Order(ent.Asc(vendorinvoice.FieldDecidedAt)).Limit(1000).All(ctx)
	if err != nil {
		return 0, err
	}
	byTenant := map[uuid.UUID][]*ent.VendorInvoice{}
	for _, r := range rows {
		byTenant[r.TenantID] = append(byTenant[r.TenantID], r)
	}
	paid := 0
	for tenantID, list := range byTenant {
		tctx := tenantguard.With(ctx, tenantID)
		for start := 0; start < len(list); start += 200 {
			chunk := list[start:min(start+200, len(list))]
			ids := make([]uuid.UUID, len(chunk))
			for i, inv := range chunk {
				ids[i] = inv.ID
			}
			bills, err := s.treasury.BillsBySource(tctx, tenantID, ids)
			if err != nil {
				s.log.Warn("vendor invoice payments: treasury lookup failed", zap.String("tenant_id", tenantID.String()), zap.Error(err))
				break
			}
			byInvoice := map[uuid.UUID]treasury.SourceBill{}
			for _, b := range bills {
				byInvoice[b.SourceReferenceID] = b
			}
			for _, inv := range chunk {
				b, ok := byInvoice[inv.ID]
				if !ok || (inv.Metadata["treasury_bill_status"] == b.Status && b.Status != "paid") {
					continue
				}
				meta := maps.Clone(inv.Metadata)
				if meta == nil {
					meta = map[string]any{}
				}
				meta["treasury_bill_number"], meta["treasury_bill_status"] = b.BillNumber, b.Status
				u := s.client.VendorInvoice.Update().Where(vendorinvoice.ID(inv.ID), vendorinvoice.StatusEQ(vendorinvoice.StatusApproved)).SetMetadata(meta)
				if b.Status == "paid" {
					u.SetStatus(vendorinvoice.StatusPaid).SetPaidAt(b.UpdatedAt)
				}
				if n, err := u.Save(tctx); err == nil && n > 0 && b.Status == "paid" {
					paid++
				}
			}
		}
	}
	return paid, nil
}

// monthsTouched counts the calendar months a period runs into (1 Jan to 31 Jan is 1).
func monthsTouched(from, to time.Time) int {
	n := (to.Year()-from.Year())*12 + int(to.Month()) - int(from.Month()) + 1
	if n < 1 {
		return 1
	}
	return n
}
