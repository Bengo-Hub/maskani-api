package billing

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var nairobi, _ = time.LoadLocation("Africa/Nairobi")

func d(v float64) decimal.Decimal { return decimal.NewFromFloat(v) }

// SRDD 8.2: unit B07, three bedroom, October 2026 = 6,450.
func TestWorkedExampleB07(t *testing.T) {
	sc, water, garbage, sink := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	prop := uuid.New()
	charges := []Charge{
		{ID: sc, Code: "service_charge", Name: "Service charge", Basis: "per_unit_type", Frequency: "monthly", Proration: "days", Priority: 10, TaxExempt: true},
		{ID: water, Code: "water", Name: "Water", Basis: "metered", Frequency: "monthly", Priority: 20, TariffKind: "flat", TaxExempt: true},
		{ID: garbage, Code: "garbage", Name: "Garbage collection", Basis: "fixed", Frequency: "monthly", Proration: "days", Priority: 30, TaxExempt: true},
		{ID: sink, Code: "sinking_fund", Name: "Sinking fund", Basis: "fixed", Frequency: "monthly", Proration: "days", Priority: 50, TaxExempt: true},
	}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, nairobi)
	rates := []Rate{
		{ChargeTypeID: sc, Scope: "unit_type", UnitType: "2br_apartment", Amount: d(3500), EffectiveFrom: from},
		{ChargeTypeID: sc, Scope: "unit_type", UnitType: "3br_apartment", Amount: d(4500), EffectiveFrom: from},
		{ChargeTypeID: water, Scope: "tenant", Amount: d(150), EffectiveFrom: from},
		{ChargeTypeID: garbage, Scope: "tenant", Amount: d(300), EffectiveFrom: from},
		{ChargeTypeID: sink, Scope: "property", PropertyID: &prop, Amount: d(300), EffectiveFrom: from},
	}
	u := UnitInfo{ID: uuid.New(), Code: "B07", PropertyID: prop, UnitType: "3br_apartment",
		Consumption: map[string]decimal.Decimal{"water": d(9)}, ReadingNote: map[string]string{"water": "1,284 less 1,275"}}
	p, _ := MonthPeriod("2026-10", nairobi)
	lines := ComputeUnit(charges, rates, u, p)
	_, _, total := Totals(lines)
	if !total.Equal(d(6450)) {
		t.Fatalf("total = %s, want 6450 (lines %+v)", total, lines)
	}
	if len(lines) != 4 || lines[1].ChargeCode != "water" || !lines[1].Amount.Equal(d(1350)) {
		t.Fatalf("unexpected lines %+v", lines)
	}
}

func TestRateSpecificityAndDates(t *testing.T) {
	c := uuid.New()
	unitID, prop := uuid.New(), uuid.New()
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, nairobi)
	newer := time.Date(2026, 9, 1, 0, 0, 0, 0, nairobi)
	rates := []Rate{
		{ChargeTypeID: c, Scope: "tenant", Amount: d(100), EffectiveFrom: old},
		{ChargeTypeID: c, Scope: "tenant", Amount: d(120), EffectiveFrom: newer},
		{ChargeTypeID: c, Scope: "unit", UnitID: &unitID, Amount: d(999), EffectiveFrom: old},
	}
	u := UnitInfo{ID: uuid.New(), PropertyID: prop}
	if r, _ := ResolveRate(rates, c, u, time.Date(2026, 8, 1, 0, 0, 0, 0, nairobi)); !r.Amount.Equal(d(100)) {
		t.Fatalf("August rate = %s, want 100", r.Amount)
	}
	if r, _ := ResolveRate(rates, c, u, time.Date(2026, 10, 1, 0, 0, 0, 0, nairobi)); !r.Amount.Equal(d(120)) {
		t.Fatalf("October rate = %s, want 120", r.Amount)
	}
	u.ID = unitID
	if r, _ := ResolveRate(rates, c, u, time.Date(2026, 10, 1, 0, 0, 0, 0, nairobi)); !r.Amount.Equal(d(999)) {
		t.Fatalf("unit rate = %s, want 999", r.Amount)
	}
}

// Mavoko-style block tariff: 120 for the first 6 m3, 150 above.
func TestBlockTariff(t *testing.T) {
	blocks := []TariffBlock{{From: 0, To: 6, Rate: 120}, {From: 6, To: 0, Rate: 150}}
	if got := BlockCharge(d(9), blocks); !got.Equal(d(6*120 + 3*150)) {
		t.Fatalf("block charge = %s", got)
	}
	if got := BlockCharge(d(4), blocks); !got.Equal(d(480)) {
		t.Fatalf("block charge low = %s", got)
	}
}

func TestProrationFromHandover(t *testing.T) {
	c := uuid.New()
	charges := []Charge{{ID: c, Code: "service_charge", Name: "Service charge", Basis: "fixed", Frequency: "monthly", Proration: "days", TaxExempt: true}}
	rates := []Rate{{ChargeTypeID: c, Scope: "tenant", Amount: d(3100), EffectiveFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, nairobi)}}
	p, _ := MonthPeriod("2026-10", nairobi)
	u := UnitInfo{ID: uuid.New(), ActiveFrom: time.Date(2026, 10, 22, 10, 0, 0, 0, nairobi)}
	lines := ComputeUnit(charges, rates, u, p)
	if len(lines) != 1 || !lines[0].Amount.Equal(d(1000)) {
		t.Fatalf("prorated lines %+v, want 1000 (10 of 31 days)", lines)
	}
}

func TestPercentageAndOptIn(t *testing.T) {
	sc, sink, park := uuid.New(), uuid.New(), uuid.New()
	charges := []Charge{
		{ID: sc, Code: "service_charge", Name: "Service charge", Basis: "fixed", Frequency: "monthly", TaxExempt: true},
		{ID: sink, Code: "sinking_fund", Name: "Sinking fund", Basis: "percentage", PercentageOf: "service_charge", Frequency: "monthly", TaxExempt: true},
		{ID: park, Code: "extra_parking", Name: "Extra parking", Basis: "fixed", Frequency: "monthly", AppliesScope: "opt_in", TaxExempt: true},
	}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, nairobi)
	rates := []Rate{
		{ChargeTypeID: sc, Scope: "tenant", Amount: d(4000), EffectiveFrom: from},
		{ChargeTypeID: sink, Scope: "tenant", Amount: d(10), EffectiveFrom: from},
		{ChargeTypeID: park, Scope: "tenant", Amount: d(1500), EffectiveFrom: from},
	}
	p, _ := MonthPeriod("2026-11", nairobi)
	u := UnitInfo{ID: uuid.New(), OptIn: map[string]decimal.Decimal{"extra_parking": d(2)}}
	_, _, total := Totals(ComputeUnit(charges, rates, u, p))
	if !total.Equal(d(4000 + 400 + 3000)) {
		t.Fatalf("total = %s", total)
	}
	u.OptIn = nil
	_, _, total = Totals(ComputeUnit(charges, rates, u, p))
	if !total.Equal(d(4400)) {
		t.Fatalf("total without opt-in = %s", total)
	}
}
