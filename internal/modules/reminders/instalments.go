package reminders

import (
	"context"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/instalment"
	"github.com/bengobox/maskani-api/internal/ent/maskaniuseroutlet"
	"github.com/bengobox/maskani-api/internal/ent/salecontract"
	"github.com/bengobox/maskani-api/internal/ent/unit"
	"github.com/bengobox/maskani-api/internal/ent/unitaccount"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/modules/accounts"
	"github.com/bengobox/maskani-api/internal/modules/register"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// instalmentOffsets are the days from the due date a buyer is reminded: three days before, on
// the day, then a week and two weeks late (SRDD instalment reminders).
var instalmentOffsets = []int{3, 0, -7, -14}

var unpaidStatuses = []instalment.Status{instalment.StatusInvoiced, instalment.StatusPartiallyPaid, instalment.StatusOverdue}

// RunInstalments sends the instalment reminders due today (working hours only), marks unpaid
// instalments past their date overdue, and moves contracts in and out of default after their
// grace days. Returns how many reminders went out.
func (s *Service) RunInstalments(ctx context.Context, tenants []uuid.UUID, now time.Time) (int, error) {
	sent := 0
	for _, tid := range tenants {
		tctx := tenantguard.With(ctx, tid)
		if err := s.markOverdue(tctx, now); err != nil {
			s.log.Warn("instalments overdue", zap.String("tenant", tid.String()), zap.Error(err))
		}
		if err := s.defaults(tctx, tid, now); err != nil {
			s.log.Warn("contract defaults", zap.String("tenant", tid.String()), zap.Error(err))
		}
		if s.InHours(now) {
			n, err := s.remindInstalments(tctx, tid, now)
			if err != nil {
				s.log.Warn("instalment reminders", zap.String("tenant", tid.String()), zap.Error(err))
			}
			sent += n
		}
	}
	return sent, nil
}

func (s *Service) day(now time.Time) time.Time {
	l := now.In(s.loc)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, s.loc)
}

// markOverdue flags invoiced instalments with nothing paid once their date has passed.
func (s *Service) markOverdue(ctx context.Context, now time.Time) error {
	return s.client.Instalment.Update().Where(instalment.StatusEQ(instalment.StatusInvoiced),
		instalment.DueDateLT(s.day(now))).SetStatus(instalment.StatusOverdue).Exec(ctx)
}

func (s *Service) remindInstalments(ctx context.Context, tenantID uuid.UUID, now time.Time) (int, error) {
	today := s.day(now)
	// Live contracts, then their instalments in the reminder window, then the accounts: three reads.
	contracts, err := s.client.SaleContract.Query().Where(salecontract.StatusIn(salecontract.StatusActive, salecontract.StatusInDefault)).
		Limit(2000).All(ctx)
	if err != nil || len(contracts) == 0 {
		return 0, err
	}
	cids := make([]uuid.UUID, len(contracts))
	for i, c := range contracts {
		cids[i] = c.ID
	}
	rows, err := s.client.Instalment.Query().Where(instalment.ContractIDIn(cids...), instalment.StatusIn(unpaidStatuses...),
		instalment.DueDateGTE(today.AddDate(0, 0, -15)), instalment.DueDateLTE(today.AddDate(0, 0, 4))).
		Limit(1000).All(ctx)
	if err != nil || len(rows) == 0 {
		return 0, err
	}
	byID := map[uuid.UUID]*ent.SaleContract{}
	var accIDs []uuid.UUID
	for _, c := range contracts {
		byID[c.ID] = c
		if c.UnitAccountID != nil {
			accIDs = append(accIDs, *c.UnitAccountID)
		}
	}
	accs := map[uuid.UUID]*ent.UnitAccount{}
	if len(accIDs) > 0 {
		list, err := s.client.UnitAccount.Query().Where(unitaccount.IDIn(accIDs...)).WithFund().All(ctx)
		if err != nil {
			return 0, err
		}
		for _, a := range list {
			accs[a.ID] = a
		}
	}
	sent := 0
	for _, in := range rows {
		d := in.DueDate.In(s.loc)
		offset := int(time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, s.loc).Sub(today).Hours() / 24)
		if !slices.Contains(instalmentOffsets, offset) || remindedAt(in, offset) {
			continue
		}
		c := byID[in.ContractID]
		if c == nil || c.UnitAccountID == nil || accs[*c.UnitAccountID] == nil {
			continue
		}
		acc := accs[*c.UnitAccountID]
		paybill := ""
		if acc.Edges.Fund != nil {
			paybill = acc.Edges.Fund.PaybillShortcode
		}
		payload := map[string]any{
			"instalment_id": in.ID, "contract_id": c.ID, "contract_number": c.ContractNumber, "seq": in.Seq,
			"amount": in.Amount.StringFixed(2), "outstanding": in.Amount.Sub(in.PaidAmount).StringFixed(2),
			"due_date": d.Format("2006-01-02"), "days_to_due": offset, "account_id": acc.ID, "account_ref": acc.AccountRef,
			"name": acc.CustomerName, "phone": acc.CustomerPhone, "email": accounts.CustomerEmail(acc), "paybill": paybill,
			"pay_account":   accounts.PayInstructionFor(acc.Edges.Fund, acc.AccountRef).Account,
			"pay_reference": accounts.PayInstructionFor(acc.Edges.Fund, acc.AccountRef).Reference,
		}
		if err := events.Publish(ctx, s.client.OutboxEvent, tenantID, in.ID.String(), events.InstalmentDue, payload); err != nil {
			return sent, err
		}
		meta := map[string]any{}
		for k, v := range in.Metadata {
			meta[k] = v
		}
		meta["reminded"] = append(remindedList(in), offset)
		if err := s.client.Instalment.UpdateOneID(in.ID).SetMetadata(meta).Exec(ctx); err != nil {
			return sent, err
		}
		sent++
	}
	return sent, nil
}

func remindedList(in *ent.Instalment) []int {
	raw, _ := in.Metadata["reminded"].([]any)
	out := make([]int, 0, len(raw)+1)
	for _, v := range raw {
		if f, ok := v.(float64); ok {
			out = append(out, int(f))
		}
	}
	return out
}

func remindedAt(in *ent.Instalment, offset int) bool {
	return slices.Contains(remindedList(in), offset)
}

// defaults moves a contract into default when an instalment is still unpaid its grace days after
// falling due, and back to active once nothing is that late. Sales, finance and the manager hear
// of a new default.
func (s *Service) defaults(ctx context.Context, tenantID uuid.UUID, now time.Time) error {
	today := s.day(now)
	contracts, err := s.client.SaleContract.Query().Where(salecontract.StatusIn(salecontract.StatusActive, salecontract.StatusInDefault)).
		Limit(2000).All(ctx)
	if err != nil || len(contracts) == 0 {
		return err
	}
	ids := make([]uuid.UUID, len(contracts))
	for i, c := range contracts {
		ids[i] = c.ID
	}
	// The oldest unpaid due date per contract, in one read.
	var late []struct {
		ContractID uuid.UUID `json:"contract_id"`
		Oldest     time.Time `json:"oldest"`
	}
	if err := s.client.Instalment.Query().Where(instalment.ContractIDIn(ids...), instalment.StatusIn(unpaidStatuses...)).
		GroupBy(instalment.FieldContractID).Aggregate(ent.As(ent.Min(instalment.FieldDueDate), "oldest")).Scan(ctx, &late); err != nil {
		return err
	}
	oldest := map[uuid.UUID]time.Time{}
	for _, l := range late {
		oldest[l.ContractID] = l.Oldest
	}
	for _, c := range contracts {
		o, has := oldest[c.ID]
		inDefault := has && o.AddDate(0, 0, c.GraceDays).Before(today)
		switch {
		case inDefault && c.Status == salecontract.StatusActive:
			if err := c.Update().SetStatus(salecontract.StatusInDefault).Exec(ctx); err != nil {
				return err
			}
			_ = s.client.Unit.UpdateOneID(c.UnitID).SetSaleStatus(unit.SaleStatusInDefault).Exec(ctx)
			payload := map[string]any{"contract_id": c.ID, "contract_number": c.ContractNumber, "property_id": c.PropertyID,
				"oldest_due": o.In(s.loc).Format("2006-01-02"), "grace_days": c.GraceDays}
			if rs, _, err := register.PropertyResponders(ctx, s.client, c.PropertyID, []maskaniuseroutlet.PropertyRole{
				maskaniuseroutlet.PropertyRoleSales, maskaniuseroutlet.PropertyRoleFinance, maskaniuseroutlet.PropertyRolePropertyManager}, 10); err == nil && len(rs) > 0 {
				payload["responders"] = rs
			}
			if err := events.Publish(ctx, s.client.OutboxEvent, tenantID, c.ID.String(), events.SaleContractDefaulted, payload); err != nil {
				return err
			}
		case !inDefault && c.Status == salecontract.StatusInDefault:
			if err := c.Update().SetStatus(salecontract.StatusActive).Exec(ctx); err != nil {
				return err
			}
			_ = s.client.Unit.UpdateOneID(c.UnitID).SetSaleStatus(unit.SaleStatusUnderAgreement).Exec(ctx)
		}
	}
	return nil
}
