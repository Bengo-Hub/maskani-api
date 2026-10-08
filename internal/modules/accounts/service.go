// Package accounts opens unit accounts (one per unit and fund) and keeps their paybill routes
// registered in treasury, so a payment to account "B07" or "S-B07" settles only that account.
package accounts

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/fund"
	"github.com/bengobox/maskani-api/internal/ent/unitaccount"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/modules/treasury"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
	"github.com/bengobox/maskani-api/internal/shared/secure"
)

// Service manages unit accounts.
type Service struct {
	client   *ent.Client
	treasury *treasury.Client
	log      *zap.Logger
}

// NewService creates the accounts service.
func NewService(client *ent.Client, tc *treasury.Client, log *zap.Logger) *Service {
	return &Service{client: client, treasury: tc, log: log.Named("accounts")}
}

// Party is who the account bills.
type Party struct {
	ID    uuid.UUID
	Name  string
	Phone string
	Email string // kept in metadata.customer_email for message delivery
}

// withEmail returns metadata carrying the bill-to email (merged, so other keys survive).
func (p *Party) withEmail(meta map[string]any) map[string]any {
	out := make(map[string]any, len(meta)+1)
	for k, v := range meta {
		out[k] = v
	}
	if e := strings.TrimSpace(p.Email); e != "" {
		out["customer_email"] = strings.ToLower(e)
	}
	return out
}

// CustomerEmail returns the bill-to email stored on the account, if any.
func CustomerEmail(acc *ent.UnitAccount) string {
	e, _ := acc.Metadata["customer_email"].(string)
	return e
}

// Ensure returns the unit's account in the fund, creating it (and its outbox event) if missing.
// The treasury route is registered after commit; RegisterPending retries any that failed.
func (s *Service) Ensure(ctx context.Context, unit *ent.Unit, fundCode string, p *Party) (*ent.UnitAccount, error) {
	f, err := s.client.Fund.Query().Where(fund.Code(fundCode)).Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("fund %q not configured: %w", fundCode, err)
	}
	acc, err := s.client.UnitAccount.Query().
		Where(unitaccount.UnitID(unit.ID), unitaccount.FundID(f.ID)).Only(ctx)
	if err == nil {
		if p != nil && acc.PrimaryPartyID == nil {
			acc, err = acc.Update().SetPrimaryPartyID(p.ID).SetCustomerName(p.Name).SetCustomerPhone(p.Phone).
				SetMetadata(p.withEmail(acc.Metadata)).Save(ctx)
		}
		return acc, err
	}
	if !ent.IsNotFound(err) {
		return nil, err
	}

	ref := secure.AccountRef(f.AccountPrefix, unit.Code)
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	c := tx.UnitAccount.Create().SetUnitID(unit.ID).SetFundID(f.ID).SetAccountRef(ref)
	if p != nil {
		c.SetPrimaryPartyID(p.ID).SetCustomerName(p.Name).SetCustomerPhone(p.Phone).SetMetadata(p.withEmail(nil))
	}
	acc, err = c.Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsConstraintError(err) {
			return s.client.UnitAccount.Query().Where(unitaccount.UnitID(unit.ID), unitaccount.FundID(f.ID)).Only(ctx)
		}
		return nil, err
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	if err := events.Publish(ctx, tx.OutboxEvent, tenantID, acc.ID.String(), events.UnitAccountCreated, map[string]any{
		"account_id": acc.ID, "account_ref": ref, "unit_id": unit.ID, "unit_code": unit.Code, "fund": f.Code,
		"paybill": f.PaybillShortcode,
	}); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.register(ctx, acc, f)
	return acc, nil
}

func (s *Service) register(ctx context.Context, acc *ent.UnitAccount, f *ent.Fund) {
	if !s.treasury.Enabled() {
		return
	}
	tenantID, ok := tenantguard.TenantID(ctx)
	if !ok {
		tenantID = acc.TenantID
	}
	err := s.treasury.RegisterC2BRoute(ctx, tenantID, treasury.C2BRoute{
		AccountRef: acc.AccountRef, Shortcode: f.PaybillShortcode, ReferenceID: acc.ID.String(), Fund: f.Code,
	})
	if err != nil {
		s.log.Warn("c2b route registration failed; will retry", zap.String("account_ref", acc.AccountRef), zap.Error(err))
		return
	}
	_ = s.client.UnitAccount.UpdateOneID(acc.ID).SetC2bRouteRegisteredAt(time.Now()).Exec(ctx)
}

// RegisterPending retries route registration for accounts that have none yet, in bounded batches.
// Called by a once-per-fleet job with a system context.
func (s *Service) RegisterPending(ctx context.Context, batch int) (int, error) {
	rows, err := s.client.UnitAccount.Query().
		Where(unitaccount.C2bRouteRegisteredAtIsNil(), unitaccount.StatusEQ(unitaccount.StatusActive)).
		WithFund().Order(ent.Asc(unitaccount.FieldCreatedAt)).Limit(batch).All(ctx)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, acc := range rows {
		if acc.Edges.Fund == nil {
			continue
		}
		s.register(tenantguard.With(ctx, acc.TenantID), acc, acc.Edges.Fund)
		done++
	}
	return done, nil
}

// SetBalance stores treasury's balance as the display cache.
func (s *Service) SetBalance(ctx context.Context, accountID uuid.UUID, balance decimal.Decimal, lastPaid *time.Time) error {
	u := s.client.UnitAccount.UpdateOneID(accountID).SetBalance(balance).SetBalanceSyncedAt(time.Now())
	if lastPaid != nil {
		u.SetLastPaymentAt(*lastPaid)
	}
	return u.Exec(ctx)
}

// Refresh pulls the account's ledger from treasury and updates the cached balance.
func (s *Service) Refresh(ctx context.Context, acc *ent.UnitAccount) (*treasury.AccountLedger, error) {
	tenantID, _ := tenantguard.TenantID(ctx)
	led, err := s.treasury.Ledger(ctx, tenantID, acc.AccountRef, 50)
	if err != nil {
		return nil, err
	}
	_ = s.SetBalance(ctx, acc.ID, led.Balance, led.LastPaidAt)
	return led, nil
}
