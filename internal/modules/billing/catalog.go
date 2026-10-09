package billing

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/chargerate"
	"github.com/bengobox/maskani-api/internal/ent/chargetype"
	"github.com/bengobox/maskani-api/internal/ent/fund"
	"github.com/bengobox/maskani-api/internal/ent/unitaccount"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/accounts"
	"github.com/bengobox/maskani-api/internal/modules/treasury"
	"github.com/bengobox/maskani-api/internal/platform/realtime"
)

// Service is the billing domain service.
type Service struct {
	client   *ent.Client
	treasury *treasury.Client
	accounts *accounts.Service
	loc      *time.Location
	log      *zap.Logger
	rt       realtime.Publisher
	rdb      redis.UniversalClient
}

// SetRealtime sets the publisher for run progress hints (nil disables them).
func (s *Service) SetRealtime(p realtime.Publisher) { s.rt = p }

// SetRedis lets run issuing take a fleet-wide lease per run, so a resumed run never overlaps one
// still going on another pod (nil, as in local runs, issues without the lease).
func (s *Service) SetRedis(rdb redis.UniversalClient) { s.rdb = rdb }

// NewService creates the billing service.
func NewService(client *ent.Client, tc *treasury.Client, acc *accounts.Service, loc *time.Location, log *zap.Logger) *Service {
	return &Service{client: client, treasury: tc, accounts: acc, loc: loc, log: log.Named("billing")}
}

// ListFunds returns the tenant's funds.
func (s *Service) ListFunds(ctx context.Context) ([]*ent.Fund, error) {
	return s.client.Fund.Query().Order(ent.Asc(fund.FieldCode)).All(ctx)
}

// FundInput updates a fund's banking details.
type FundInput struct {
	Name                  *string    `json:"name"`
	TreasuryBankAccountID *uuid.UUID `json:"treasury_bank_account_id"`
	PaybillShortcode      *string    `json:"paybill_shortcode"`
	AccountPrefix         *string    `json:"account_prefix"`
	CostCenterCode        *string    `json:"cost_center_code"`
	IncomeAccountCode     *string    `json:"income_account_code"`
	ReceivableAccountCode *string    `json:"receivable_account_code"`
}

// UpdateFund edits a fund. Changing the paybill re-queues route registration for its accounts.
func (s *Service) UpdateFund(ctx context.Context, id uuid.UUID, in FundInput) (*ent.Fund, error) {
	u := s.client.Fund.UpdateOneID(id)
	if in.Name != nil {
		u.SetName(*in.Name)
	}
	if in.TreasuryBankAccountID != nil {
		u.SetTreasuryBankAccountID(*in.TreasuryBankAccountID)
	}
	if in.PaybillShortcode != nil {
		u.SetPaybillShortcode(strings.TrimSpace(*in.PaybillShortcode))
	}
	if in.AccountPrefix != nil {
		u.SetAccountPrefix(strings.ToUpper(strings.TrimSpace(*in.AccountPrefix)))
	}
	if in.CostCenterCode != nil {
		u.SetCostCenterCode(*in.CostCenterCode)
	}
	if in.IncomeAccountCode != nil {
		u.SetIncomeAccountCode(*in.IncomeAccountCode)
	}
	if in.ReceivableAccountCode != nil {
		u.SetReceivableAccountCode(*in.ReceivableAccountCode)
	}
	f, err := u.Save(ctx)
	if err == nil && in.PaybillShortcode != nil {
		_, _ = s.client.UnitAccount.Update().Where(unitaccount.FundID(id)).ClearC2bRouteRegisteredAt().Save(ctx)
	}
	return f, err
}

// ChargeView is a charge type with its current rates.
type ChargeView struct {
	*ent.ChargeType
	Rates []*ent.ChargeRate `json:"rates"`
}

// ListCharges returns the catalogue with rates effective now or later.
func (s *Service) ListCharges(ctx context.Context, includeInactive bool) ([]ChargeView, error) {
	q := s.client.ChargeType.Query()
	if !includeInactive {
		q = q.Where(chargetype.Active(true))
	}
	cts, err := q.WithRates(func(r *ent.ChargeRateQuery) {
		r.Where(chargerate.Or(chargerate.EffectiveToIsNil(), chargerate.EffectiveToGT(time.Now()))).
			Order(ent.Desc(chargerate.FieldEffectiveFrom))
	}).Order(ent.Asc(chargetype.FieldSort), ent.Asc(chargetype.FieldName)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ChargeView, len(cts))
	for i, c := range cts {
		out[i] = ChargeView{ChargeType: c, Rates: c.Edges.Rates}
	}
	return out, nil
}

// ChargeInput creates or edits a charge type.
type ChargeInput struct {
	Code              *string        `json:"code"`
	Name              *string        `json:"name"`
	Description       *string        `json:"description"`
	ChargeGroup       *string        `json:"charge_group"`
	Basis             *string        `json:"basis"`
	Frequency         *string        `json:"frequency"`
	EventTrigger      *string        `json:"event_trigger"`
	AppliesTo         map[string]any `json:"applies_to"`
	BillTo            *string        `json:"bill_to"`
	Reassignable      *bool          `json:"reassignable"`
	FundCode          *string        `json:"fund_code"`
	LedgerAccountCode *string        `json:"ledger_account_code"`
	CostCenterCode    *string        `json:"cost_center_code"`
	VATRate           *float64       `json:"vat_rate"`
	TaxExempt         *bool          `json:"tax_exempt"`
	ETIMSItemCode     *string        `json:"etims_item_code"`
	WHTApplicable     *bool          `json:"wht_applicable"`
	Proration         *string        `json:"proration"`
	Penalty           map[string]any `json:"penalty"`
	Priority          *int           `json:"allocation_priority"`
	PercentageOfCode  *string        `json:"percentage_of_code"`
	TariffKind        *string        `json:"tariff_kind"`
	Sort              *int           `json:"sort"`
	Active            *bool          `json:"active"`
}

// CreateCharge adds a custom charge to the catalogue.
func (s *Service) CreateCharge(ctx context.Context, in ChargeInput) (*ent.ChargeType, error) {
	if in.Code == nil || in.Name == nil || in.ChargeGroup == nil || in.Basis == nil {
		return nil, httpx.Invalid("code, name, charge_group and basis are required")
	}
	c := s.client.ChargeType.Create().SetCode(strings.ToLower(strings.TrimSpace(*in.Code))).SetName(*in.Name).
		SetChargeGroup(chargetype.ChargeGroup(*in.ChargeGroup)).SetBasis(chargetype.Basis(*in.Basis))
	applyCharge(c.Mutation(), in)
	return c.Save(ctx)
}

// UpdateCharge edits a charge (rename, enable or disable, settings). Rates change only through
// new dated rates.
func (s *Service) UpdateCharge(ctx context.Context, id uuid.UUID, in ChargeInput) (*ent.ChargeType, error) {
	u := s.client.ChargeType.UpdateOneID(id)
	if in.Name != nil {
		u.SetName(*in.Name)
	}
	if in.ChargeGroup != nil {
		u.SetChargeGroup(chargetype.ChargeGroup(*in.ChargeGroup))
	}
	if in.Basis != nil {
		u.SetBasis(chargetype.Basis(*in.Basis))
	}
	applyCharge(u.Mutation(), in)
	return u.Save(ctx)
}

func applyCharge(m *ent.ChargeTypeMutation, in ChargeInput) {
	if in.Description != nil {
		m.SetDescription(*in.Description)
	}
	if in.Frequency != nil {
		m.SetFrequency(chargetype.Frequency(*in.Frequency))
	}
	if in.EventTrigger != nil {
		m.SetEventTrigger(*in.EventTrigger)
	}
	if in.AppliesTo != nil {
		m.SetAppliesTo(in.AppliesTo)
	}
	if in.BillTo != nil {
		m.SetBillTo(chargetype.BillTo(*in.BillTo))
	}
	if in.Reassignable != nil {
		m.SetReassignable(*in.Reassignable)
	}
	if in.FundCode != nil {
		m.SetFundCode(*in.FundCode)
	}
	if in.LedgerAccountCode != nil {
		m.SetLedgerAccountCode(*in.LedgerAccountCode)
	}
	if in.CostCenterCode != nil {
		m.SetCostCenterCode(*in.CostCenterCode)
	}
	if in.VATRate != nil {
		m.SetVatRate(*in.VATRate)
	}
	if in.TaxExempt != nil {
		m.SetTaxExempt(*in.TaxExempt)
	}
	if in.ETIMSItemCode != nil {
		m.SetEtimsItemCode(*in.ETIMSItemCode)
	}
	if in.WHTApplicable != nil {
		m.SetWhtApplicable(*in.WHTApplicable)
	}
	if in.Proration != nil {
		m.SetProration(chargetype.Proration(*in.Proration))
	}
	if in.Penalty != nil {
		m.SetPenalty(in.Penalty)
	}
	if in.Priority != nil {
		m.SetAllocationPriority(*in.Priority)
	}
	if in.PercentageOfCode != nil {
		m.SetPercentageOfCode(*in.PercentageOfCode)
	}
	if in.TariffKind != nil {
		m.SetTariffKind(chargetype.TariffKind(*in.TariffKind))
	}
	if in.Sort != nil {
		m.SetSort(*in.Sort)
	}
	if in.Active != nil {
		m.SetActive(*in.Active)
	}
}

// RateInput adds a dated rate.
type RateInput struct {
	Scope            string        `json:"scope"`
	PropertyID       *uuid.UUID    `json:"property_id"`
	UnitType         string        `json:"unit_type"`
	UnitID           *uuid.UUID    `json:"unit_id"`
	Amount           float64       `json:"amount"`
	Tariff           []TariffBlock `json:"tariff"`
	FixedMeterCharge float64       `json:"fixed_meter_charge"`
	EffectiveFrom    time.Time     `json:"effective_from"`
	Notes            string        `json:"notes"`
}

// AddRate adds a rate effective from a date and closes the previous rate at the same scope, so the
// rate history stays continuous and already-issued invoices are untouched.
func (s *Service) AddRate(ctx context.Context, chargeID, actor uuid.UUID, in RateInput) (*ent.ChargeRate, error) {
	if in.Amount < 0 || in.EffectiveFrom.IsZero() {
		return nil, httpx.Invalid("amount must not be negative and effective_from is required")
	}
	scope := chargerate.Scope(in.Scope)
	if in.Scope == "" {
		scope = chargerate.ScopeTenant
	}
	if err := chargerate.ScopeValidator(scope); err != nil {
		return nil, httpx.Invalid("invalid scope")
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	prev := tx.ChargeRate.Update().Where(chargerate.ChargeTypeID(chargeID), chargerate.ScopeEQ(scope),
		chargerate.EffectiveToIsNil(), chargerate.EffectiveFromLT(in.EffectiveFrom))
	switch scope {
	case chargerate.ScopeProperty:
		if in.PropertyID == nil {
			_ = tx.Rollback()
			return nil, httpx.Invalid("property_id is required for a property rate")
		}
		prev.Where(chargerate.PropertyID(*in.PropertyID))
	case chargerate.ScopeUnitType:
		if in.UnitType == "" {
			_ = tx.Rollback()
			return nil, httpx.Invalid("unit_type is required for a unit type rate")
		}
		prev.Where(chargerate.UnitType(in.UnitType))
	case chargerate.ScopeUnit:
		if in.UnitID == nil {
			_ = tx.Rollback()
			return nil, httpx.Invalid("unit_id is required for a unit rate")
		}
		prev.Where(chargerate.UnitID(*in.UnitID))
	}
	if _, err := prev.SetEffectiveTo(in.EffectiveFrom).Save(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	c := tx.ChargeRate.Create().SetChargeTypeID(chargeID).SetScope(scope).
		SetAmount(decimal.NewFromFloat(in.Amount)).SetFixedMeterCharge(decimal.NewFromFloat(in.FixedMeterCharge)).
		SetEffectiveFrom(in.EffectiveFrom).SetCreatedBy(actor)
	if in.PropertyID != nil {
		c.SetPropertyID(*in.PropertyID)
	}
	if in.UnitType != "" {
		c.SetUnitType(in.UnitType)
	}
	if in.UnitID != nil {
		c.SetUnitID(*in.UnitID)
	}
	if len(in.Tariff) > 0 {
		raw, _ := json.Marshal(in.Tariff)
		var blocks []map[string]any
		_ = json.Unmarshal(raw, &blocks)
		c.SetTariff(blocks)
	}
	if in.Notes != "" {
		c.SetNotes(in.Notes)
	}
	r, err := c.Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return r, tx.Commit()
}

// loadCatalog converts the fund's active charges and rates into computation inputs.
func (s *Service) loadCatalog(ctx context.Context, fundCode string) ([]Charge, []Rate, error) {
	cts, err := s.client.ChargeType.Query().
		Where(chargetype.Active(true), chargetype.FundCode(fundCode)).WithRates().All(ctx)
	if err != nil {
		return nil, nil, err
	}
	var charges []Charge
	var rates []Rate
	for _, c := range cts {
		ch := Charge{ID: c.ID, Code: c.Code, Name: c.Name, Basis: string(c.Basis), Frequency: string(c.Frequency),
			VATRate: c.VatRate, TaxExempt: c.TaxExempt, Proration: string(c.Proration), Priority: c.AllocationPriority,
			PercentageOf: c.PercentageOfCode, TariffKind: string(c.TariffKind), ETIMSCode: c.EtimsItemCode}
		if scope, ok := c.AppliesTo["scope"].(string); ok {
			ch.AppliesScope = scope
		}
		if ids, ok := c.AppliesTo["ids"].([]any); ok {
			for _, v := range ids {
				if sv, ok := v.(string); ok {
					ch.AppliesIDs = append(ch.AppliesIDs, sv)
				}
			}
		}
		charges = append(charges, ch)
		for _, r := range c.Edges.Rates {
			rt := Rate{ChargeTypeID: c.ID, Scope: string(r.Scope), PropertyID: r.PropertyID, UnitType: r.UnitType,
				UnitID: r.UnitID, Amount: r.Amount, FixedMeterCharge: r.FixedMeterCharge, EffectiveFrom: r.EffectiveFrom,
				EffectiveTo: r.EffectiveTo}
			if len(r.Tariff) > 0 {
				raw, _ := json.Marshal(r.Tariff)
				_ = json.Unmarshal(raw, &rt.Tariff)
			}
			rates = append(rates, rt)
		}
	}
	return charges, rates, nil
}
