// Package billing computes and issues monthly bills. Computation is pure (this file) so every rule
// is unit-tested; issuing goes through treasury, which owns the invoices.
package billing

import (
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Charge is a resolved catalogue entry for computation.
type Charge struct {
	ID               uuid.UUID
	Code             string
	Name             string
	Basis            string // fixed, per_unit_type, per_sqm, entitlement, metered, percentage, one_off
	Frequency        string
	AppliesScope     string // tenant, properties, unit_types, units, opt_in
	AppliesIDs       []string
	VATRate          float64
	TaxExempt        bool
	Proration        string // none, days, full_month
	Priority         int
	PercentageOf     string
	TariffKind       string // flat, block
	ETIMSCode        string
}

// Rate is a dated rate at a scope.
type Rate struct {
	ChargeTypeID     uuid.UUID
	Scope            string // tenant, property, unit_type, unit
	PropertyID       *uuid.UUID
	UnitType         string
	UnitID           *uuid.UUID
	Amount           decimal.Decimal
	Tariff           []TariffBlock
	FixedMeterCharge decimal.Decimal
	EffectiveFrom    time.Time
	EffectiveTo      *time.Time
}

// TariffBlock is one band of a block tariff: consumption from From up to To (0 = no upper bound).
type TariffBlock struct {
	From float64 `json:"from"`
	To   float64 `json:"to"`
	Rate float64 `json:"rate"`
}

// UnitInfo is what computation needs about a unit.
type UnitInfo struct {
	ID          uuid.UUID
	Code        string
	PropertyID  uuid.UUID
	UnitType    string
	SizeSqm     decimal.Decimal
	Entitlement decimal.Decimal
	// ActiveFrom is when estate billing starts (handover); zero = whole period.
	ActiveFrom time.Time
	// OptIn lists opted-in charges with quantity (second parking bay).
	OptIn map[string]decimal.Decimal
	// Consumption is metered usage for the period per charge code (water: m3).
	Consumption map[string]decimal.Decimal
	// ReadingNote describes the reading behind a metered line ("1,284 less 1,275").
	ReadingNote map[string]string
}

// Line is one invoice line.
type Line struct {
	ChargeCode  string          `json:"charge_code"`
	Description string          `json:"description"`
	Quantity    decimal.Decimal `json:"quantity"`
	Rate        decimal.Decimal `json:"rate"`
	Amount      decimal.Decimal `json:"amount"`
	TaxRate     float64         `json:"tax_rate"`
	Tax         decimal.Decimal `json:"tax"`
	ETIMSCode   string          `json:"etims_item_code,omitempty"`
	Priority    int             `json:"priority"`
}

// Period is a billing month.
type Period struct {
	Start time.Time // first day 00:00
	End   time.Time // first day of the next month
}

// MonthPeriod returns the period for "YYYY-MM" in loc.
func MonthPeriod(ym string, loc *time.Location) (Period, error) {
	t, err := time.ParseInLocation("2006-01", ym, loc)
	if err != nil {
		return Period{}, err
	}
	return Period{Start: t, End: t.AddDate(0, 1, 0)}, nil
}

// Days is the number of days in the period.
func (p Period) Days() int { return int(p.End.Sub(p.Start).Hours()/24 + 0.5) }

// dueInPeriod reports whether a charge of this frequency falls in the period's month.
func dueInPeriod(freq string, p Period) bool {
	m := int(p.Start.Month())
	switch freq {
	case "monthly":
		return true
	case "quarterly":
		return m%3 == 1
	case "half_yearly":
		return m == 1 || m == 7
	case "annual":
		return m == 1
	}
	return false
}

func applies(c Charge, u UnitInfo) bool {
	switch c.AppliesScope {
	case "", "tenant":
		return true
	case "properties":
		return contains(c.AppliesIDs, u.PropertyID.String())
	case "unit_types":
		return contains(c.AppliesIDs, u.UnitType)
	case "units":
		return contains(c.AppliesIDs, u.ID.String())
	case "opt_in":
		_, ok := u.OptIn[c.Code]
		return ok
	}
	return false
}

// ResolveRate picks the most specific rate effective on day for the unit: unit, then unit type,
// then property, then tenant; ties go to the latest effective_from.
func ResolveRate(rates []Rate, chargeID uuid.UUID, u UnitInfo, day time.Time) (Rate, bool) {
	rank := func(r Rate) int {
		switch r.Scope {
		case "unit":
			if r.UnitID != nil && *r.UnitID == u.ID {
				return 4
			}
		case "unit_type":
			if r.UnitType != "" && r.UnitType == u.UnitType {
				return 3
			}
		case "property":
			if r.PropertyID != nil && *r.PropertyID == u.PropertyID {
				return 2
			}
		case "tenant":
			return 1
		}
		return 0
	}
	var best Rate
	bestRank := 0
	for _, r := range rates {
		if r.ChargeTypeID != chargeID || r.EffectiveFrom.After(day) || (r.EffectiveTo != nil && !r.EffectiveTo.After(day)) {
			continue
		}
		rk := rank(r)
		if rk == 0 {
			continue
		}
		if rk > bestRank || (rk == bestRank && r.EffectiveFrom.After(best.EffectiveFrom)) {
			best, bestRank = r, rk
		}
	}
	return best, bestRank > 0
}

// BlockCharge prices consumption across tariff bands.
func BlockCharge(consumption decimal.Decimal, blocks []TariffBlock) decimal.Decimal {
	total := decimal.Zero
	for _, b := range blocks {
		lo := decimal.NewFromFloat(b.From)
		if consumption.LessThanOrEqual(lo) {
			continue
		}
		hi := consumption
		if b.To > 0 {
			if top := decimal.NewFromFloat(b.To); top.LessThan(hi) {
				hi = top
			}
		}
		if hi.GreaterThan(lo) {
			total = total.Add(hi.Sub(lo).Mul(decimal.NewFromFloat(b.Rate)))
		}
	}
	return total.Round(2)
}

// prorate scales a monthly amount by the share of the period the unit was billable.
func prorate(amount decimal.Decimal, mode string, u UnitInfo, p Period) decimal.Decimal {
	if u.ActiveFrom.IsZero() || !u.ActiveFrom.After(p.Start) {
		return amount
	}
	if !u.ActiveFrom.Before(p.End) {
		return decimal.Zero
	}
	switch mode {
	case "days":
		start := time.Date(u.ActiveFrom.Year(), u.ActiveFrom.Month(), u.ActiveFrom.Day(), 0, 0, 0, 0, p.Start.Location())
		days := int(p.End.Sub(start).Hours()/24 + 0.5)
		return amount.Mul(decimal.NewFromInt(int64(days))).Div(decimal.NewFromInt(int64(p.Days()))).Round(2)
	case "full_month":
		return amount
	}
	return amount
}

// ComputeUnit returns the period's lines for one unit. Metered charges with no consumption and
// zero-amount lines are left out; percentage charges are computed after the others.
func ComputeUnit(charges []Charge, rates []Rate, u UnitInfo, p Period) []Line {
	var lines []Line
	byCode := map[string]decimal.Decimal{}
	var pct []Charge
	for _, c := range charges {
		if !dueInPeriod(c.Frequency, p) || !applies(c, u) {
			continue
		}
		if c.Basis == "percentage" {
			pct = append(pct, c)
			continue
		}
		r, ok := ResolveRate(rates, c.ID, u, p.Start)
		if !ok {
			continue
		}
		qty := decimal.NewFromInt(1)
		rate := r.Amount
		amount := decimal.Zero
		desc := c.Name
		switch c.Basis {
		case "fixed", "per_unit_type", "one_off":
			amount = r.Amount
		case "per_sqm":
			qty, amount = u.SizeSqm, u.SizeSqm.Mul(r.Amount)
		case "entitlement":
			qty, amount = u.Entitlement, u.Entitlement.Mul(r.Amount)
		case "metered":
			used, ok := u.Consumption[c.Code]
			if !ok {
				continue
			}
			qty = used
			if c.TariffKind == "block" && len(r.Tariff) > 0 {
				amount = BlockCharge(used, r.Tariff)
				if used.IsPositive() {
					rate = amount.Div(used).Round(2)
				}
			} else {
				amount = used.Mul(r.Amount)
			}
			amount = amount.Add(r.FixedMeterCharge)
			if note := u.ReadingNote[c.Code]; note != "" {
				desc = c.Name + ": " + note
			}
		}
		if q, ok := u.OptIn[c.Code]; ok && c.AppliesScope == "opt_in" {
			qty = q
			amount = r.Amount.Mul(q)
		}
		if c.Basis != "metered" {
			amount = prorate(amount, c.Proration, u, p)
		}
		amount = amount.Round(2)
		if !amount.IsPositive() {
			continue
		}
		byCode[c.Code] = amount
		lines = append(lines, newLine(c, desc, qty, rate, amount))
	}
	for _, c := range pct {
		base, ok := byCode[c.PercentageOf]
		if !ok {
			continue
		}
		r, ok := ResolveRate(rates, c.ID, u, p.Start)
		if !ok {
			continue
		}
		amount := base.Mul(r.Amount).Div(decimal.NewFromInt(100)).Round(2)
		if amount.IsPositive() {
			lines = append(lines, newLine(c, c.Name, decimal.NewFromInt(1), amount, amount))
		}
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].Priority < lines[j].Priority })
	return lines
}

func newLine(c Charge, desc string, qty, rate, amount decimal.Decimal) Line {
	l := Line{ChargeCode: c.Code, Description: desc, Quantity: qty, Rate: rate, Amount: amount, ETIMSCode: c.ETIMSCode, Priority: c.Priority}
	if !c.TaxExempt && c.VATRate > 0 {
		l.TaxRate = c.VATRate
		l.Tax = amount.Mul(decimal.NewFromFloat(c.VATRate)).Div(decimal.NewFromInt(100)).Round(2)
	}
	return l
}

// Totals sums lines.
func Totals(lines []Line) (subtotal, tax, total decimal.Decimal) {
	for _, l := range lines {
		subtotal = subtotal.Add(l.Amount)
		tax = tax.Add(l.Tax)
	}
	return subtotal, tax, subtotal.Add(tax)
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
