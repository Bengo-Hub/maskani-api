package collections

import (
	"context"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/fund"
	"github.com/bengobox/maskani-api/internal/ent/unitaccount"
	"github.com/bengobox/maskani-api/internal/modules/accounts"
	"github.com/bengobox/maskani-api/internal/modules/settings"
	"github.com/bengobox/maskani-api/internal/modules/treasury"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// KindLateCharge marks a late payment charge in the bill's treasury metadata (and on the ledger),
// so later charges never count it as overdue principal.
const KindLateCharge = "late_charge"

// lateChargeKey on an account's metadata holds the month (YYYY-MM) it was last looked at, so each
// account costs one ledger read a month however often the job runs.
const lateChargeKey = "late_charge_month"

// OverdueBase is what a late charge is worked on: the unpaid, uncredited part of every bill more
// than graceDays past due, leaving out earlier late charges so the charge never compounds.
func OverdueBase(led *treasury.AccountLedger, today time.Time, graceDays int) decimal.Decimal {
	base := decimal.Zero
	for _, inv := range led.Invoices {
		if inv.Kind == KindLateCharge || !inv.DueDate.AddDate(0, 0, graceDays).Before(today) {
			continue
		}
		if owed := inv.TotalAmount.Sub(inv.AmountPaid).Sub(inv.AmountCredited); owed.IsPositive() {
			base = base.Add(owed)
		}
	}
	return base
}

// RunLateCharges charges each owing account in the estates that switched the late charge on, once
// a month from the estate's day. Bounded by limit accounts per run; the job runs hourly. Returns
// how many charges were raised.
func (s *Service) RunLateCharges(ctx context.Context, tenants []uuid.UUID, now time.Time, loc *time.Location, limit int) (int, error) {
	if s.treasury == nil || !s.treasury.Enabled() {
		return 0, nil
	}
	today := now.In(loc)
	month := today.Format("2006-01")
	charged, looked := 0, 0
	for _, tid := range tenants {
		if looked >= limit {
			break
		}
		tctx := tenantguard.With(ctx, tid)
		st, err := s.client.TenantSetting.Query().Only(tctx)
		if err != nil {
			continue // no settings row: nothing switched on
		}
		lc := settings.LateChargeOf(st)
		if !lc.Enabled || today.Day() < lc.Day {
			continue
		}
		accs, err := s.client.UnitAccount.Query().Where(unitaccount.StatusEQ(unitaccount.StatusActive),
			unitaccount.BalanceGT(decimal.Zero), unitaccount.HasFundWith(fund.CodeIn(lc.Funds...)),
			func(sel *entsql.Selector) {
				sel.Where(entsql.Or(
					entsql.Not(sqljson.HasKey(sel.C(unitaccount.FieldMetadata), sqljson.Path(lateChargeKey))),
					entsql.Not(sqljson.ValueEQ(sel.C(unitaccount.FieldMetadata), month, sqljson.Path(lateChargeKey)))))
			}).WithFund().Order(ent.Asc(unitaccount.FieldID)).Limit(limit - looked).All(tctx)
		if err != nil {
			return charged, err
		}
		for _, acc := range accs {
			looked++
			ok, err := s.chargeLate(tctx, tid, acc, lc, today, month)
			if err != nil {
				s.log.Warn("late charge", zap.String("account", acc.AccountRef), zap.Error(err))
				continue // the account stays unmarked, so the next run tries again
			}
			if ok {
				charged++
			}
		}
	}
	return charged, nil
}

// chargeLate raises this month's late charge for one account if it has overdue principal, then
// marks the account as looked at for the month. The treasury reference is derived from the account
// and month, so a retry or a second pod finds the same invoice instead of raising another.
func (s *Service) chargeLate(ctx context.Context, tenantID uuid.UUID, acc *ent.UnitAccount, lc settings.LateCharge, today time.Time, month string) (bool, error) {
	led, err := s.treasury.Ledger(ctx, tenantID, acc.AccountRef, 200)
	if err != nil {
		return false, err
	}
	amount := lc.Amount(OverdueBase(led, today, lc.GraceDays))
	charged := false
	if amount.IsPositive() {
		ref := uuid.NewSHA1(acc.ID, []byte("late:"+month))
		label := "Late payment charge, " + today.Format("January 2006")
		req := treasury.CreateInvoiceRequest{
			CustomerName: acc.CustomerName, CustomerPhone: acc.CustomerPhone, InvoiceType: "standard",
			InvoiceDate: today, DueDate: today.AddDate(0, 0, 14), Currency: "KES",
			ReferenceID: &ref, ReferenceType: treasury.RefLateCharge, Notes: label,
			Lines: []treasury.InvoiceLine{{Description: label, ItemType: "service", Quantity: 1, UnitPrice: amount.InexactFloat64()}},
			Metadata: map[string]any{"account_ref": acc.AccountRef, "unit_account_id": acc.ID.String(), "kind": KindLateCharge,
				"period": label, "source_service": treasury.SourceService},
		}
		if acc.Edges.Fund != nil {
			req.Metadata["fund"] = acc.Edges.Fund.Code
			if acc.Edges.Fund.TreasuryBankAccountID != nil {
				req.SettlementAccountID = acc.Edges.Fund.TreasuryBankAccountID
			}
			pay := accounts.PayInstructionFor(acc.Edges.Fund, acc.AccountRef)
			if pay.Paybill != "" {
				req.Notes += ". Pay to paybill " + pay.Paybill + ", account " + pay.Account + "."
			}
		}
		if _, err := s.treasury.IssueInvoice(ctx, tenantID, req); err != nil {
			return false, err
		}
		charged = true
		if _, err := s.accounts.Refresh(ctx, acc); err != nil {
			s.log.Warn("balance refresh after late charge", zap.String("account", acc.AccountRef))
		}
	}
	meta := map[string]any{}
	for k, v := range acc.Metadata {
		meta[k] = v
	}
	meta[lateChargeKey] = month
	return charged, s.client.UnitAccount.UpdateOneID(acc.ID).SetMetadata(meta).Exec(ctx)
}
