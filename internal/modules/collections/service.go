// Package collections covers statements, the paybill suspense queue and portal payments. Money is
// always treasury's: maskani starts intents, assigns unmatched payments and reads ledgers.
package collections

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/property"
	"github.com/bengobox/maskani-api/internal/ent/unit"
	"github.com/bengobox/maskani-api/internal/ent/unitaccount"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/accounts"
	"github.com/bengobox/maskani-api/internal/modules/treasury"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
	"github.com/bengobox/maskani-api/internal/shared/secure"
)

// Service is the collections service.
type Service struct {
	client   *ent.Client
	treasury *treasury.Client
	accounts *accounts.Service
	log      *zap.Logger
}

// NewService creates the collections service.
func NewService(client *ent.Client, tc *treasury.Client, acc *accounts.Service, log *zap.Logger) *Service {
	return &Service{client: client, treasury: tc, accounts: acc, log: log.Named("collections")}
}

// Statement is an account with treasury's ledger.
type Statement struct {
	Account *ent.UnitAccount        `json:"account"`
	Ledger  *treasury.AccountLedger `json:"ledger"`
}

// Statement returns the account's ledger from treasury and refreshes the cached balance.
func (s *Service) Statement(ctx context.Context, accountID uuid.UUID) (*Statement, error) {
	acc, err := s.client.UnitAccount.Query().Where(unitaccount.ID(accountID)).WithFund().WithUnit().Only(ctx)
	if err != nil {
		return nil, err
	}
	led, err := s.accounts.Refresh(ctx, acc)
	if err != nil {
		s.log.Warn("ledger unavailable", zap.String("account", acc.AccountRef), zap.Error(err))
		return &Statement{Account: acc}, nil
	}
	return &Statement{Account: acc, Ledger: led}, nil
}

// ListAccounts returns accounts for a property (or all visible), with cached balances.
func (s *Service) ListAccounts(ctx context.Context, propertyID *uuid.UUID, scope []uuid.UUID, all bool, onlyOwing bool, limit int) ([]*ent.UnitAccount, error) {
	q := s.client.UnitAccount.Query().Where(unitaccount.StatusEQ(unitaccount.StatusActive))
	if propertyID != nil {
		q = q.Where(unitaccount.HasUnitWith(unit.PropertyID(*propertyID)))
	} else if !all {
		q = q.Where(unitaccount.HasUnitWith(unit.PropertyIDIn(scope...)))
	}
	if onlyOwing {
		q = q.Where(unitaccount.BalanceGT(decimal.Zero))
	}
	return q.WithFund().WithUnit().Order(ent.Desc(unitaccount.FieldBalance), ent.Asc(unitaccount.FieldAccountRef)).
		Limit(min(max(limit, 1), 1000)).All(ctx)
}

// Suspense lists unmatched paybill payments (the treasury C2B inbox) since a date.
func (s *Service) Suspense(ctx context.Context, since time.Time) ([]treasury.C2BPayment, error) {
	tenantID, _ := tenantguard.TenantID(ctx)
	return s.treasury.SuspensePayments(ctx, tenantID, since)
}

// Assign applies an unmatched paybill payment to a unit account.
func (s *Service) Assign(ctx context.Context, transID string, accountID uuid.UUID) error {
	acc, err := s.client.UnitAccount.Get(ctx, accountID)
	if err != nil {
		return err
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	return s.treasury.AssignSuspense(ctx, tenantID, transID, acc.ID, acc.AccountRef)
}

// PayInput starts a portal or staff-initiated payment.
type PayInput struct {
	Amount  float64 `json:"amount"`
	Method  string  `json:"payment_method"`
	Phone   string  `json:"phone"`
	Gateway string  `json:"gateway"`
	Email   string  `json:"email"`
	Return  string  `json:"callback_url"`
	// Key is one per pay attempt from the client, so a double tap reuses the same intent while a
	// retry after a cancelled prompt starts a new one.
	Key string `json:"idempotency_key"`
}

// Pay creates an account_payment intent through the tenant's gateway; treasury allocates it oldest
// due first when it settles. With no amount the full outstanding balance is requested.
func (s *Service) Pay(ctx context.Context, accountID uuid.UUID, in PayInput) (*treasury.IntentResponse, error) {
	acc, err := s.client.UnitAccount.Query().Where(unitaccount.ID(accountID)).WithFund().WithUnit().Only(ctx)
	if err != nil {
		return nil, err
	}
	amount := decimal.NewFromFloat(in.Amount)
	if !amount.IsPositive() {
		if led, err := s.accounts.Refresh(ctx, acc); err == nil {
			amount = led.Balance
		} else {
			amount = acc.Balance
		}
	}
	if !amount.IsPositive() {
		return nil, httpx.Invalid("nothing is due on this account")
	}
	method := in.Method
	if method == "" {
		method = "mpesa"
	}
	phone := secure.NormalizePhone(in.Phone)
	if phone == "" {
		phone = acc.CustomerPhone
	}
	desc := fmt.Sprintf("%s payment", acc.AccountRef)
	tenantID, _ := tenantguard.TenantID(ctx)
	// treasury returns the existing intent for a known reference_id whatever its status, so each
	// attempt needs its own reference; the account travels in metadata (unit_account_id).
	key := strings.TrimSpace(in.Key)
	if key == "" || len(key) > 64 {
		key = uuid.NewString()
	}
	ref := "MSK-PAY-" + acc.ID.String()[:8] + "-" + key
	req := treasury.IntentRequest{
		ReferenceID: ref, ReferenceType: treasury.RefAccountPayment, PaymentMethod: method,
		Currency: "KES", Amount: amount.Round(2), Description: &desc, Gateway: in.Gateway, CallbackURL: in.Return,
		IdempotencyKey: ref,
		Metadata: map[string]any{"account_ref": acc.AccountRef, "unit_account_id": acc.ID.String(),
			"entity_id": acc.ID.String(), "fund": acc.Edges.Fund.Code, "source_service": treasury.SourceService},
	}
	if phone != "" {
		req.PhoneNumber = &phone
	}
	if in.Email != "" {
		req.CustomerEmail = &in.Email
	}
	if acc.Edges.Unit != nil {
		if p, err := s.client.Property.Query().Where(property.ID(acc.Edges.Unit.PropertyID)).Only(ctx); err == nil && p.OutletID != nil {
			req.OutletID = p.OutletID
		}
	}
	return s.treasury.CreateIntent(ctx, tenantID, req)
}
