package works

import (
	"context"
	"maps"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/maskaniuseroutlet"
	"github.com/bengobox/maskani-api/internal/ent/vendor"
	"github.com/bengobox/maskani-api/internal/ent/vendorcontract"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/register"
	"github.com/bengobox/maskani-api/internal/modules/treasury"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// ContractRenewalDue tells the property's managers a vendor contract ends soon.
const ContractRenewalDue = "vendor_contract.renewal_due"

// SetTreasury gives the service the treasury client (supplier lookup, vendor bills).
func (s *Service) SetTreasury(tc *treasury.Client) { s.treasury = tc }

// TreasuryVendors finds payable suppliers in treasury by name, so a vendor is linked to the
// supplier its bills are raised against instead of a typed id.
func (s *Service) TreasuryVendors(ctx context.Context, search string) ([]treasury.Vendor, error) {
	search = strings.TrimSpace(search)
	if len(search) < 2 {
		return nil, httpx.Invalid("type at least two letters of the supplier's name")
	}
	if s.treasury == nil || !s.treasury.Enabled() {
		return nil, httpx.Unavailable("the accounts service is not set up")
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	out, err := s.treasury.Vendors(ctx, tenantID, search, 20)
	if err != nil {
		return nil, httpx.Unavailable("the accounts service is not answering; try again shortly")
	}
	return out, nil
}

// LinkTreasuryVendor links a vendor to its supplier in treasury (picked from TreasuryVendors).
func (s *Service) LinkTreasuryVendor(ctx context.Context, id, treasuryVendorID uuid.UUID, name string) (*ent.Vendor, error) {
	v, err := s.client.Vendor.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	meta := maps.Clone(v.Metadata)
	if meta == nil {
		meta = map[string]any{}
	}
	meta["treasury_vendor_name"] = strings.TrimSpace(name)
	return v.Update().SetTreasuryVendorID(treasuryVendorID).SetMetadata(meta).Save(ctx)
}

// ContractInput creates or changes a vendor's service contract for one property.
type ContractInput struct {
	PropertyID        uuid.UUID        `json:"property_id"`
	ContractNumber    string           `json:"contract_number"`
	ServiceCategory   string           `json:"service_category"`
	Scope             string           `json:"scope"`
	FeeBasis          string           `json:"fee_basis"` // fixed_monthly, per_visit, per_post, rate_card
	FeeAmount         decimal.Decimal  `json:"fee_amount"`
	SLA               map[string]any   `json:"sla"`             // {"response_hours": 4, "resolution_hours": 24, "visits_per_month": 8}
	ServiceCredits    []map[string]any `json:"service_credits"` // [{"breach": "missed_visit", "percent": 5}]
	StartsOn          time.Time        `json:"starts_on"`
	EndsOn            *time.Time       `json:"ends_on"`
	RenewalNoticeDays int              `json:"renewal_notice_days"`
	AutoRenew         bool             `json:"auto_renew"`
	BudgetLineCode    string           `json:"budget_line_code"`
}

func (in *ContractInput) check() error {
	in.ServiceCategory, in.ContractNumber = strings.TrimSpace(in.ServiceCategory), strings.TrimSpace(in.ContractNumber)
	switch {
	case in.PropertyID == uuid.Nil:
		return httpx.Invalid("choose the property the contract covers")
	case in.ServiceCategory == "":
		return httpx.Invalid("say which service the contract covers")
	case vendorcontract.FeeBasisValidator(vendorcontract.FeeBasis(in.FeeBasis)) != nil:
		return httpx.Invalid("fee basis must be fixed_monthly, per_visit, per_post or rate_card")
	case in.FeeAmount.IsNegative():
		return httpx.Invalid("the fee cannot be negative")
	case in.StartsOn.IsZero():
		return httpx.Invalid("give the date the contract starts")
	case in.EndsOn != nil && !in.EndsOn.After(in.StartsOn):
		return httpx.Invalid("the contract must end after it starts")
	case in.RenewalNoticeDays < 0 || in.RenewalNoticeDays > 365:
		return httpx.Invalid("renewal notice is between 0 and 365 days")
	case in.AutoRenew && in.EndsOn == nil:
		return httpx.Invalid("a contract that renews itself needs an end date")
	}
	for _, c := range in.ServiceCredits {
		p, _ := c["percent"].(float64)
		if b, _ := c["breach"].(string); b == "" || p <= 0 || p > 100 {
			return httpx.Invalid("each service credit names the breach and a percentage between 0 and 100")
		}
	}
	if in.RenewalNoticeDays == 0 {
		in.RenewalNoticeDays = 30
	}
	return nil
}

// CreateContract adds a service contract for an active vendor.
func (s *Service) CreateContract(ctx context.Context, vendorID uuid.UUID, in ContractInput) (*ent.VendorContract, error) {
	if err := in.check(); err != nil {
		return nil, err
	}
	v, err := s.client.Vendor.Get(ctx, vendorID)
	if err != nil {
		return nil, err
	}
	if v.Status != vendor.StatusActive {
		return nil, httpx.Conflict("this vendor is not active")
	}
	return s.client.VendorContract.Create().SetVendorID(v.ID).SetPropertyID(in.PropertyID).SetContractNumber(in.ContractNumber).
		SetServiceCategory(in.ServiceCategory).SetScope(in.Scope).SetFeeBasis(vendorcontract.FeeBasis(in.FeeBasis)).
		SetFeeAmount(in.FeeAmount).SetSLA(in.SLA).SetServiceCredits(in.ServiceCredits).SetStartsOn(in.StartsOn).
		SetNillableEndsOn(in.EndsOn).SetRenewalNoticeDays(in.RenewalNoticeDays).SetAutoRenew(in.AutoRenew).
		SetBudgetLineCode(in.BudgetLineCode).SetStatus(vendorcontract.StatusActive).Save(ctx)
}

// UpdateContract changes a live contract's terms (a terminated or expired one is kept as it was).
func (s *Service) UpdateContract(ctx context.Context, id uuid.UUID, in ContractInput) (*ent.VendorContract, error) {
	c, err := s.client.VendorContract.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if c.Status != vendorcontract.StatusActive && c.Status != vendorcontract.StatusDraft {
		return nil, httpx.Conflict("only a live contract can change; add a new one instead")
	}
	in.PropertyID = c.PropertyID
	if err := in.check(); err != nil {
		return nil, err
	}
	u := c.Update().SetContractNumber(in.ContractNumber).SetServiceCategory(in.ServiceCategory).SetScope(in.Scope).
		SetFeeBasis(vendorcontract.FeeBasis(in.FeeBasis)).SetFeeAmount(in.FeeAmount).SetSLA(in.SLA).
		SetServiceCredits(in.ServiceCredits).SetStartsOn(in.StartsOn).SetRenewalNoticeDays(in.RenewalNoticeDays).
		SetAutoRenew(in.AutoRenew).SetBudgetLineCode(in.BudgetLineCode)
	if in.EndsOn != nil {
		u.SetEndsOn(*in.EndsOn)
	} else {
		u.ClearEndsOn()
	}
	return u.Save(ctx)
}

// TerminateContract ends a contract early with the reason.
func (s *Service) TerminateContract(ctx context.Context, id, by uuid.UUID, reason string) (*ent.VendorContract, error) {
	c, err := s.client.VendorContract.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if c.Status != vendorcontract.StatusActive {
		return nil, httpx.Conflict("only an active contract can be terminated")
	}
	if reason = strings.TrimSpace(reason); reason == "" {
		return nil, httpx.Invalid("give the reason for ending the contract")
	}
	meta := maps.Clone(c.Metadata)
	if meta == nil {
		meta = map[string]any{}
	}
	meta["termination"] = map[string]any{"reason": reason, "by": by.String(), "at": time.Now().Format(time.RFC3339)}
	return c.Update().SetStatus(vendorcontract.StatusTerminated).SetEndsOn(time.Now()).SetMetadata(meta).Save(ctx)
}

// ContractProperty returns the property a contract covers (for scope checks).
func (s *Service) ContractProperty(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	c, err := s.client.VendorContract.Query().Where(vendorcontract.ID(id)).Select(vendorcontract.FieldPropertyID).Only(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	return c.PropertyID, nil
}

// vendorContracts lists a vendor's contracts within the caller's properties, newest first.
func (s *Service) vendorContracts(ctx context.Context, vendorID uuid.UUID, scope []uuid.UUID, all bool) ([]*ent.VendorContract, error) {
	q := s.client.VendorContract.Query().Where(vendorcontract.VendorID(vendorID))
	if !all {
		q = q.Where(vendorcontract.PropertyIDIn(scope...))
	}
	return q.Order(ent.Desc(vendorcontract.FieldStartsOn)).Limit(200).All(ctx)
}

// RenewContracts is the daily contract job: a contract within its notice period is announced once
// to the property's managers; one past its end either renews for the same length (auto_renew,
// recorded in metadata) or expires. Served by the (tenant_id, ends_on) index, 500 at a time.
func (s *Service) RenewContracts(ctx context.Context, tenants []uuid.UUID) (alerted, renewed, expired int, err error) {
	if len(tenants) == 0 {
		return 0, 0, 0, nil
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	horizon := today.AddDate(0, 0, 366)
	rows, err := s.client.VendorContract.Query().Where(vendorcontract.TenantIDIn(tenants...), vendorcontract.StatusEQ(vendorcontract.StatusActive),
		vendorcontract.EndsOnNotNil(), vendorcontract.EndsOnLT(horizon)).WithVendor().Order(ent.Asc(vendorcontract.FieldEndsOn)).Limit(500).All(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	for _, c := range rows {
		tctx := tenantguard.With(ctx, c.TenantID)
		end := c.EndsOn.UTC().Truncate(24 * time.Hour)
		meta := maps.Clone(c.Metadata)
		if meta == nil {
			meta = map[string]any{}
		}
		switch {
		case end.Before(today) && c.AutoRenew:
			term := c.EndsOn.Sub(c.StartsOn)
			hist, _ := meta["renewals"].([]any)
			meta["renewals"] = append(hist, map[string]any{"from": c.EndsOn.Format("2006-01-02"), "to": c.EndsOn.Add(term).Format("2006-01-02")})
			delete(meta, "renewal_alerted_for")
			if err := c.Update().SetStartsOn(*c.EndsOn).SetEndsOn(c.EndsOn.Add(term)).SetMetadata(meta).Exec(tctx); err == nil {
				renewed++
			}
		case end.Before(today):
			if err := c.Update().SetStatus(vendorcontract.StatusExpired).Exec(tctx); err == nil {
				expired++
			}
		case !end.After(today.AddDate(0, 0, c.RenewalNoticeDays)):
			key := c.EndsOn.Format("2006-01-02")
			if c.Metadata["renewal_alerted_for"] == key {
				continue
			}
			payload := map[string]any{"contract_id": c.ID, "contract_number": c.ContractNumber, "service": c.ServiceCategory,
				"ends_on": key, "auto_renew": c.AutoRenew, "days_left": int(end.Sub(today).Hours() / 24), "property_id": c.PropertyID}
			if c.Edges.Vendor != nil {
				payload["vendor"], payload["vendor_id"] = c.Edges.Vendor.Name, c.Edges.Vendor.ID
			}
			if rs, name, rerr := register.PropertyResponders(tctx, s.client, c.PropertyID,
				[]maskaniuseroutlet.PropertyRole{maskaniuseroutlet.PropertyRolePropertyManager}, 10); rerr == nil {
				payload["property"] = name
				if len(rs) > 0 {
					payload["responders"] = rs
				}
			}
			meta["renewal_alerted_for"] = key
			tx, terr := s.client.Tx(tctx)
			if terr != nil {
				continue
			}
			if err := tx.VendorContract.UpdateOneID(c.ID).SetMetadata(meta).Exec(tctx); err != nil {
				_ = tx.Rollback()
				continue
			}
			if err := events.Publish(tctx, tx.OutboxEvent, c.TenantID, c.ID.String()+":renewal:"+key, ContractRenewalDue, payload); err != nil {
				_ = tx.Rollback()
				continue
			}
			if tx.Commit() == nil {
				alerted++
			}
		}
	}
	return alerted, renewed, expired, nil
}
