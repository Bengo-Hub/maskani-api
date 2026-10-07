package sales

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/instalment"
	"github.com/bengobox/maskani-api/internal/ent/instalmentschedule"
	"github.com/bengobox/maskani-api/internal/ent/pricelist"
	"github.com/bengobox/maskani-api/internal/ent/pricelistitem"
	"github.com/bengobox/maskani-api/internal/ent/reservation"
	"github.com/bengobox/maskani-api/internal/ent/salecontract"
	"github.com/bengobox/maskani-api/internal/ent/unit"
	"github.com/bengobox/maskani-api/internal/ent/unitparty"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/accounts"
	"github.com/bengobox/maskani-api/internal/modules/sequence"
	"github.com/bengobox/maskani-api/internal/modules/treasury"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// Service is the sales service.
type Service struct {
	client   *ent.Client
	treasury *treasury.Client
	accounts *accounts.Service
	seq      *sequence.Allocator
	loc      *time.Location
	log      *zap.Logger
}

// NewService creates the sales service.
func NewService(client *ent.Client, tc *treasury.Client, acc *accounts.Service, seq *sequence.Allocator, loc *time.Location, log *zap.Logger) *Service {
	return &Service{client: client, treasury: tc, accounts: acc, seq: seq, loc: loc, log: log.Named("sales")}
}

// PriceListInput creates a price list with items.
type PriceListInput struct {
	PropertyID    uuid.UUID       `json:"property_id"`
	Name          string          `json:"name"`
	Phase         string          `json:"phase"`
	EffectiveFrom time.Time       `json:"effective_from"`
	Activate      bool            `json:"activate"`
	Items         []PriceItemSpec `json:"items"`
}

// PriceItemSpec is one price line.
type PriceItemSpec struct {
	UnitType       string     `json:"unit_type"`
	UnitID         *uuid.UUID `json:"unit_id"`
	Price          float64    `json:"price"`
	ReservationFee float64    `json:"reservation_fee"`
	DepositPct     float64    `json:"deposit_pct"`
	MaxTermMonths  int        `json:"max_term_months"`
}

// CreatePriceList stores a price list; activating it retires the property's previous active list.
func (s *Service) CreatePriceList(ctx context.Context, actor uuid.UUID, in PriceListInput) (*ent.PriceList, error) {
	if in.Name == "" || len(in.Items) == 0 {
		return nil, httpx.Invalid("name and at least one item are required")
	}
	if in.EffectiveFrom.IsZero() {
		in.EffectiveFrom = time.Now()
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	status := pricelist.StatusDraft
	if in.Activate {
		status = pricelist.StatusActive
		if _, err := tx.PriceList.Update().Where(pricelist.PropertyID(in.PropertyID), pricelist.StatusEQ(pricelist.StatusActive)).
			SetStatus(pricelist.StatusRetired).SetEffectiveTo(in.EffectiveFrom).Save(ctx); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	}
	pl, err := tx.PriceList.Create().SetPropertyID(in.PropertyID).SetName(in.Name).SetPhase(in.Phase).
		SetEffectiveFrom(in.EffectiveFrom).SetStatus(status).SetCreatedBy(actor).Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	for _, it := range in.Items {
		if it.Price <= 0 {
			_ = tx.Rollback()
			return nil, httpx.Invalid("every item needs a price")
		}
		dep := it.DepositPct
		if dep <= 0 {
			dep = 20
		}
		term := it.MaxTermMonths
		if term <= 0 {
			term = 24
		}
		c := tx.PriceListItem.Create().SetPriceListID(pl.ID).SetUnitType(it.UnitType).
			SetPrice(decimal.NewFromFloat(it.Price)).SetReservationFee(decimal.NewFromFloat(it.ReservationFee)).
			SetDepositPct(dep).SetMaxTermMonths(term)
		if it.UnitID != nil {
			c.SetUnitID(*it.UnitID)
		}
		if err := c.Exec(ctx); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	}
	return pl, tx.Commit()
}

// ListPriceLists returns a property's price lists with items.
func (s *Service) ListPriceLists(ctx context.Context, propertyID uuid.UUID) ([]*ent.PriceList, error) {
	return s.client.PriceList.Query().Where(pricelist.PropertyID(propertyID)).WithItems().
		Order(ent.Desc(pricelist.FieldEffectiveFrom)).All(ctx)
}

// Availability is a unit with its current price.
type Availability struct {
	*ent.Unit
	Price          *decimal.Decimal `json:"price,omitempty"`
	ReservationFee *decimal.Decimal `json:"reservation_fee,omitempty"`
	DepositPct     float64          `json:"deposit_pct,omitempty"`
	PriceItemID    *uuid.UUID       `json:"price_list_item_id,omitempty"`
}

// Availability returns the property's units with sale status and the active price.
func (s *Service) Availability(ctx context.Context, propertyID uuid.UUID) ([]Availability, error) {
	units, err := s.client.Unit.Query().Where(unit.PropertyID(propertyID), unit.StatusEQ(unit.StatusActive),
		unit.SaleStatusNEQ(unit.SaleStatusNotForSale)).WithBlock().Order(ent.Asc(unit.FieldCode)).All(ctx)
	if err != nil {
		return nil, err
	}
	items, err := s.activeItems(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	out := make([]Availability, len(units))
	for i, u := range units {
		out[i] = Availability{Unit: u}
		if it := pickItem(items, u); it != nil {
			out[i].Price, out[i].ReservationFee, out[i].DepositPct, out[i].PriceItemID = &it.Price, &it.ReservationFee, it.DepositPct, &it.ID
		}
	}
	return out, nil
}

func (s *Service) activeItems(ctx context.Context, propertyID uuid.UUID) ([]*ent.PriceListItem, error) {
	return s.client.PriceListItem.Query().Where(pricelistitem.HasPriceListWith(
		pricelist.PropertyID(propertyID), pricelist.StatusEQ(pricelist.StatusActive))).All(ctx)
}

func pickItem(items []*ent.PriceListItem, u *ent.Unit) *ent.PriceListItem {
	var byType *ent.PriceListItem
	for _, it := range items {
		if it.UnitID != nil && *it.UnitID == u.ID {
			return it
		}
		if it.UnitID == nil && it.UnitType == u.UnitType {
			byType = it
		}
	}
	return byType
}

// ReserveInput reserves a unit for a buyer.
type ReserveInput struct {
	UnitID  uuid.UUID `json:"unit_id"`
	PartyID uuid.UUID `json:"party_id"`
	Days    int       `json:"days"`
}

// Reserve holds an available unit, opens the buyer's sales account and invoices the fee.
func (s *Service) Reserve(ctx context.Context, actor uuid.UUID, in ReserveInput) (*ent.Reservation, error) {
	u, err := s.client.Unit.Get(ctx, in.UnitID)
	if err != nil {
		return nil, err
	}
	if u.SaleStatus != unit.SaleStatusAvailable {
		return nil, httpx.Conflict("this unit is not available")
	}
	buyer, err := s.client.Party.Get(ctx, in.PartyID)
	if err != nil {
		return nil, err
	}
	items, err := s.activeItems(ctx, u.PropertyID)
	if err != nil {
		return nil, err
	}
	it := pickItem(items, u)
	if it == nil {
		return nil, httpx.Invalid("no active price for this unit")
	}
	days := in.Days
	if days <= 0 {
		days = 14
	}
	now := time.Now()
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	res, err := tx.Reservation.Create().SetUnitID(u.ID).SetPartyID(buyer.ID).SetPriceListItemID(it.ID).
		SetPrice(it.Price).SetFeeAmount(it.ReservationFee).SetReservedAt(now).SetExpiresAt(now.AddDate(0, 0, days)).
		SetCreatedBy(actor).Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsConstraintError(err) {
			return nil, httpx.Conflict("this unit already has an active reservation")
		}
		return nil, err
	}
	if err := tx.Unit.UpdateOneID(u.ID).SetSaleStatus(unit.SaleStatusReserved).Exec(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	acc, err := s.accounts.Ensure(ctx, u, "sales", &accounts.Party{ID: buyer.ID, Name: buyer.DisplayName, Phone: buyer.Phone})
	if err == nil && it.ReservationFee.IsPositive() {
		inv, ierr := s.invoice(ctx, acc, treasury.RefReservationFee, res.ID, "Reservation fee "+u.Code, it.ReservationFee, now, now.AddDate(0, 0, days))
		if ierr == nil {
			res, _ = res.Update().SetFeeTreasuryInvoiceID(inv.ID).Save(ctx)
		} else {
			s.log.Warn("reservation fee invoice failed", zap.Error(ierr))
		}
	}
	return res, nil
}

// ExpireReservations releases lapsed reservations (system job, all tenants).
func (s *Service) ExpireReservations(ctx context.Context) (int, error) {
	rows, err := s.client.Reservation.Query().
		Where(reservation.StatusIn(reservation.StatusPendingPayment, reservation.StatusActive), reservation.ExpiresAtLT(time.Now())).
		Limit(500).All(ctx)
	if err != nil {
		return 0, err
	}
	for _, r := range rows {
		tctx := tenantguard.With(ctx, r.TenantID)
		if err := s.client.Reservation.UpdateOneID(r.ID).SetStatus(reservation.StatusExpired).Exec(tctx); err != nil {
			continue
		}
		_ = s.client.Unit.Update().Where(unit.ID(r.UnitID), unit.SaleStatusEQ(unit.SaleStatusReserved)).
			SetSaleStatus(unit.SaleStatusAvailable).Exec(tctx)
	}
	return len(rows), nil
}

// ContractInput creates a sale contract.
type ContractInput struct {
	UnitID         uuid.UUID        `json:"unit_id"`
	BuyerID        uuid.UUID        `json:"buyer_id"`
	CoBuyers       []map[string]any `json:"buyers"`
	ReservationID  *uuid.UUID       `json:"reservation_id"`
	Price          float64          `json:"price"`
	Discount       float64          `json:"discount"`
	DiscountReason string           `json:"discount_reason"`
	DepositAmount  float64          `json:"deposit_amount"`
	PaymentOption  string           `json:"payment_option"`
	Frequency      string           `json:"frequency"`
	TermMonths     int              `json:"term_months"`
	InterestRate   float64          `json:"interest_rate"`
	GraceDays      int              `json:"grace_days"`
	Milestones     []Milestone      `json:"milestones"`
	Financier      map[string]any   `json:"financier"`
	BuyerAdvocate  map[string]any   `json:"buyer_advocate"`
	SellerAdvocate map[string]any   `json:"seller_advocate"`
	Notes          string           `json:"notes"`
}

// CreateContract records a draft contract, defaulting price and deposit from the active price list.
func (s *Service) CreateContract(ctx context.Context, actor uuid.UUID, in ContractInput) (*ent.SaleContract, error) {
	u, err := s.client.Unit.Get(ctx, in.UnitID)
	if err != nil {
		return nil, err
	}
	if u.SaleStatus != unit.SaleStatusAvailable && u.SaleStatus != unit.SaleStatusReserved {
		return nil, httpx.Conflict("this unit is not available for sale")
	}
	price := decimal.NewFromFloat(in.Price)
	depositPct := 20.0
	if items, err := s.activeItems(ctx, u.PropertyID); err == nil {
		if it := pickItem(items, u); it != nil {
			if !price.IsPositive() {
				price = it.Price
			}
			depositPct = it.DepositPct
		}
	}
	if !price.IsPositive() {
		return nil, httpx.Invalid("price is required")
	}
	discount := decimal.NewFromFloat(in.Discount)
	net := price.Sub(discount)
	deposit := decimal.NewFromFloat(in.DepositAmount)
	if !deposit.IsPositive() {
		deposit = net.Mul(decimal.NewFromFloat(depositPct)).Div(decimal.NewFromInt(100)).Round(0)
	}
	credit := decimal.Zero
	if in.ReservationID != nil {
		if r, err := s.client.Reservation.Get(ctx, *in.ReservationID); err == nil && r.FeePaidAt != nil {
			credit = r.FeeAmount
		}
	}
	option := salecontract.PaymentOption(in.PaymentOption)
	if in.PaymentOption == "" {
		option = salecontract.PaymentOptionInstalments
	}
	freq := salecontract.Frequency(in.Frequency)
	if in.Frequency == "" {
		freq = salecontract.FrequencyMonthly
	}
	number, err := s.seq.Next(ctx, "sale_contract", "SC")
	if err != nil {
		return nil, err
	}
	c := s.client.SaleContract.Create().SetContractNumber(number).SetPropertyID(u.PropertyID).SetUnitID(u.ID).
		SetPrimaryBuyerID(in.BuyerID).SetPrice(price).SetDiscount(discount).SetDiscountReason(in.DiscountReason).
		SetNetPrice(net).SetReservationCredit(credit).SetDepositAmount(deposit).SetPaymentOption(option).
		SetFrequency(freq).SetTermMonths(in.TermMonths).SetInterestRate(in.InterestRate).SetCreatedBy(actor).SetNotes(in.Notes)
	if in.GraceDays > 0 {
		c.SetGraceDays(in.GraceDays)
	}
	if in.ReservationID != nil {
		c.SetReservationID(*in.ReservationID)
	}
	if in.CoBuyers != nil {
		c.SetBuyers(in.CoBuyers)
	}
	if in.Financier != nil {
		c.SetFinancier(in.Financier)
	}
	if in.BuyerAdvocate != nil {
		c.SetBuyerAdvocate(in.BuyerAdvocate)
	}
	if in.SellerAdvocate != nil {
		c.SetSellerAdvocate(in.SellerAdvocate)
	}
	meta := map[string]any{}
	if len(in.Milestones) > 0 {
		meta["milestones"] = in.Milestones
	}
	c.SetMetadata(meta)
	return c.Save(ctx)
}

// Activate signs the contract: opens the buyer's sales account, writes schedule version 1, links the
// buyer to the unit and invoices instalments already due.
func (s *Service) Activate(ctx context.Context, contractID uuid.UUID, signedAt time.Time) (*ent.SaleContract, error) {
	sc, err := s.client.SaleContract.Get(ctx, contractID)
	if err != nil {
		return nil, err
	}
	if sc.Status != salecontract.StatusDraft {
		return nil, httpx.Conflict("only a draft contract can be activated")
	}
	u, err := s.client.Unit.Get(ctx, sc.UnitID)
	if err != nil {
		return nil, err
	}
	buyer, err := s.client.Party.Get(ctx, sc.PrimaryBuyerID)
	if err != nil {
		return nil, err
	}
	acc, err := s.accounts.Ensure(ctx, u, "sales", &accounts.Party{ID: buyer.ID, Name: buyer.DisplayName, Phone: buyer.Phone})
	if err != nil {
		return nil, err
	}
	if signedAt.IsZero() {
		signedAt = time.Now()
	}
	start := time.Date(signedAt.Year(), signedAt.Month(), signedAt.Day(), 0, 0, 0, 0, s.loc)
	var milestones []Milestone
	if raw, ok := sc.Metadata["milestones"].([]any); ok {
		for _, m := range raw {
			if mm, ok := m.(map[string]any); ok {
				pct, _ := mm["pct"].(float64)
				label, _ := mm["label"].(string)
				milestones = append(milestones, Milestone{Label: label, Pct: pct})
			}
		}
	}
	financed := decimal.Zero
	if v, ok := sc.Financier["amount"].(float64); ok {
		financed = decimal.NewFromFloat(v)
	}
	lines := BuildSchedule(ScheduleInput{NetPrice: sc.NetPrice, Deposit: sc.DepositAmount, ReservationCredit: sc.ReservationCredit,
		Option: string(sc.PaymentOption), Frequency: string(sc.Frequency), TermMonths: sc.TermMonths, Start: start,
		DepositDue: start, FirstDue: start.AddDate(0, 1, 0), Milestones: milestones, FinancierAmount: financed})
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	sch, err := tx.InstalmentSchedule.Create().SetContractID(sc.ID).SetVersion(1).Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	for _, l := range lines {
		if err := tx.Instalment.Create().SetScheduleID(sch.ID).SetContractID(sc.ID).SetSeq(l.Seq).
			SetKind(instalment.Kind(l.Kind)).SetDueDate(l.DueDate).SetAmount(l.Amount).SetMilestoneLabel(l.Label).Exec(ctx); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	}
	sc, err = tx.SaleContract.UpdateOneID(sc.ID).SetStatus(salecontract.StatusActive).SetSignedAt(signedAt).
		SetUnitAccountID(acc.ID).Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Unit.UpdateOneID(u.ID).SetSaleStatus(unit.SaleStatusUnderAgreement).Exec(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if sc.ReservationID != nil {
		_ = tx.Reservation.UpdateOneID(*sc.ReservationID).SetStatus(reservation.StatusConverted).SetSaleContractID(sc.ID).Exec(ctx)
	}
	if err := tx.UnitParty.Create().SetUnitID(u.ID).SetPartyID(buyer.ID).SetRole(unitparty.RoleBuyer).SetIsPrimary(true).
		SetStartDate(signedAt).SetSource(unitparty.SourceSale).Exec(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	if err := events.Publish(ctx, tx.OutboxEvent, tenantID, sc.ID.String(), events.SaleContractActivated, map[string]any{
		"contract_id": sc.ID, "contract_number": sc.ContractNumber, "unit_code": u.Code, "buyer": buyer.DisplayName,
		"phone": buyer.Phone, "account_ref": acc.AccountRef, "net_price": sc.NetPrice.StringFixed(2),
	}); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	_, _ = s.InvoiceDue(ctx, &sc.ID, time.Now().AddDate(0, 0, 7))
	return sc, nil
}

// InvoiceDue raises treasury invoices for scheduled instalments due on or before horizon (milestones
// only once released). With contractID nil it serves the daily job for the context tenant.
func (s *Service) InvoiceDue(ctx context.Context, contractID *uuid.UUID, horizon time.Time) (int, error) {
	q := s.client.Instalment.Query().Where(instalment.StatusEQ(instalment.StatusScheduled), instalment.DueDateLTE(horizon),
		instalment.HasScheduleWith(instalmentschedule.StatusEQ(instalmentschedule.StatusActive)),
		instalment.Or(instalment.KindNEQ(instalment.KindMilestone), instalment.ReleasedAtNotNil()))
	if contractID != nil {
		q = q.Where(instalment.ContractID(*contractID))
	}
	rows, err := q.Order(ent.Asc(instalment.FieldDueDate)).Limit(500).All(ctx)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, in := range rows {
		sc, err := s.client.SaleContract.Get(ctx, in.ContractID)
		if err != nil || sc.UnitAccountID == nil {
			continue
		}
		acc, err := s.client.UnitAccount.Get(ctx, *sc.UnitAccountID)
		if err != nil {
			continue
		}
		desc := fmt.Sprintf("%s %s %d, contract %s", acc.AccountRef, in.Kind, in.Seq, sc.ContractNumber)
		if in.MilestoneLabel != "" {
			desc = fmt.Sprintf("%s milestone: %s, contract %s", acc.AccountRef, in.MilestoneLabel, sc.ContractNumber)
		}
		issue := time.Now()
		if in.DueDate.Before(issue) {
			issue = in.DueDate
		}
		inv, err := s.invoice(ctx, acc, treasury.RefInstalment, in.ID, desc, in.Amount, issue, in.DueDate)
		if err != nil {
			s.log.Warn("instalment invoice failed", zap.Error(err))
			continue
		}
		_ = in.Update().SetStatus(instalment.StatusInvoiced).SetTreasuryInvoiceID(inv.ID).SetInvoiceNumber(inv.InvoiceNumber).Exec(ctx)
		_ = sc.Update().AddInvoicedTotal(in.Amount).Exec(ctx)
		done++
	}
	return done, nil
}

func (s *Service) invoice(ctx context.Context, acc *ent.UnitAccount, refType string, refID uuid.UUID, desc string, amount decimal.Decimal, issued, due time.Time) (*treasury.Invoice, error) {
	tenantID, _ := tenantguard.TenantID(ctx)
	f, err := s.client.Fund.Get(ctx, acc.FundID)
	if err != nil {
		return nil, err
	}
	amt, _ := amount.Float64()
	req := treasury.CreateInvoiceRequest{
		CustomerName: acc.CustomerName, CustomerPhone: acc.CustomerPhone, InvoiceType: "standard",
		InvoiceDate: issued, DueDate: due, Currency: "KES", ReferenceID: &refID, ReferenceType: refType,
		Notes: fmt.Sprintf("Pay to paybill %s, account %s.", f.PaybillShortcode, acc.AccountRef),
		Lines: []treasury.InvoiceLine{{Description: desc, Quantity: 1, UnitPrice: amt, ItemType: "service", ItemSKU: refType}},
		Metadata: map[string]any{"account_ref": acc.AccountRef, "unit_account_id": acc.ID.String(), "fund": f.Code,
			"source_service": treasury.SourceService},
	}
	if f.TreasuryBankAccountID != nil {
		req.SettlementAccountID = f.TreasuryBankAccountID
	}
	return s.treasury.IssueInvoice(ctx, tenantID, req)
}

// ReleaseMilestone makes a milestone instalment due now, with evidence.
func (s *Service) ReleaseMilestone(ctx context.Context, instalmentID, by uuid.UUID, evidenceKey string) (*ent.Instalment, error) {
	in, err := s.client.Instalment.Get(ctx, instalmentID)
	if err != nil {
		return nil, err
	}
	if in.Kind != instalment.KindMilestone || in.ReleasedAt != nil {
		return nil, httpx.Conflict("not an unreleased milestone")
	}
	in, err = in.Update().SetReleasedAt(time.Now()).SetReleasedBy(by).SetEvidenceKey(evidenceKey).
		SetDueDate(time.Now().AddDate(0, 0, 14)).Save(ctx)
	if err != nil {
		return nil, err
	}
	_, _ = s.InvoiceDue(ctx, &in.ContractID, time.Now().AddDate(0, 0, 30))
	return s.client.Instalment.Get(ctx, instalmentID)
}

// SyncProgress reconciles instalment and contract paid figures with treasury's ledger for a sales
// account (called by the payment consumer). Treasury allocates; this only mirrors the result.
func (s *Service) SyncProgress(ctx context.Context, accountID uuid.UUID) error {
	acc, err := s.client.UnitAccount.Get(ctx, accountID)
	if err != nil {
		return err
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	led, err := s.treasury.Ledger(ctx, tenantID, acc.AccountRef, 500)
	if err != nil {
		return err
	}
	paidByInvoice := map[uuid.UUID]decimal.Decimal{}
	for _, inv := range led.Invoices {
		paidByInvoice[inv.ID] = inv.AmountPaid
	}
	contracts, err := s.client.SaleContract.Query().Where(salecontract.UnitAccountID(acc.ID),
		salecontract.StatusIn(salecontract.StatusActive, salecontract.StatusInDefault, salecontract.StatusFullyPaid)).All(ctx)
	if err != nil {
		return err
	}
	for _, sc := range contracts {
		ins, err := s.client.Instalment.Query().Where(instalment.ContractID(sc.ID), instalment.TreasuryInvoiceIDNotNil()).All(ctx)
		if err != nil {
			return err
		}
		total := sc.ReservationCredit
		for _, in := range ins {
			paid := paidByInvoice[*in.TreasuryInvoiceID]
			total = total.Add(paid)
			st := in.Status
			switch {
			case paid.GreaterThanOrEqual(in.Amount):
				st = instalment.StatusPaid
			case paid.IsPositive():
				st = instalment.StatusPartiallyPaid
			}
			if st != in.Status || !paid.Equal(in.PaidAmount) {
				u := in.Update().SetPaidAmount(paid).SetStatus(st)
				if st == instalment.StatusPaid && in.PaidAt == nil {
					u.SetPaidAt(time.Now())
				}
				_ = u.Exec(ctx)
			}
		}
		up := sc.Update().SetPaidTotal(total)
		if total.GreaterThanOrEqual(sc.NetPrice) && sc.Status != salecontract.StatusFullyPaid {
			up.SetStatus(salecontract.StatusFullyPaid)
			_ = s.client.Unit.UpdateOneID(sc.UnitID).SetSaleStatus(unit.SaleStatusFullyPaid).Exec(ctx)
			_ = events.Publish(ctx, s.client.OutboxEvent, tenantID, sc.ID.String(), events.SaleContractFullyPaid, map[string]any{
				"contract_id": sc.ID, "contract_number": sc.ContractNumber, "account_ref": acc.AccountRef, "phone": acc.CustomerPhone,
			})
		}
		_ = up.Exec(ctx)
	}
	return nil
}

// ContractView is a contract with its active schedule.
type ContractView struct {
	*ent.SaleContract
	Instalments []*ent.Instalment `json:"instalments"`
	NextDue     *ent.Instalment   `json:"next_due,omitempty"`
	Balance     decimal.Decimal   `json:"balance"`
}

// GetContract returns a contract with its active schedule and the next instalment due.
func (s *Service) GetContract(ctx context.Context, id uuid.UUID) (*ContractView, error) {
	sc, err := s.client.SaleContract.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	ins, err := s.client.Instalment.Query().Where(instalment.ContractID(id),
		instalment.HasScheduleWith(instalmentschedule.StatusEQ(instalmentschedule.StatusActive))).
		Order(ent.Asc(instalment.FieldSeq)).All(ctx)
	if err != nil {
		return nil, err
	}
	v := &ContractView{SaleContract: sc, Instalments: ins, Balance: sc.NetPrice.Sub(sc.PaidTotal)}
	for _, in := range ins {
		if in.Status != instalment.StatusPaid && in.Status != instalment.StatusWaived {
			v.NextDue = in
			break
		}
	}
	return v, nil
}

// ListContracts returns contracts, newest first.
func (s *Service) ListContracts(ctx context.Context, propertyID *uuid.UUID, status string, limit int) ([]*ent.SaleContract, error) {
	q := s.client.SaleContract.Query()
	if propertyID != nil {
		q = q.Where(salecontract.PropertyID(*propertyID))
	}
	if status != "" {
		q = q.Where(salecontract.StatusEQ(salecontract.Status(status)))
	}
	return q.Order(ent.Desc(salecontract.FieldCreatedAt)).Limit(min(max(limit, 1), 500)).All(ctx)
}
