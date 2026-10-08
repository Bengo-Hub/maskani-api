package main

import (
	"strings"
	"testing"
	"time"

	"github.com/bengobox/maskani-api/internal/shared/secure"
)

func TestDemoUnits(t *testing.T) {
	units := demoUnits()
	if len(units) != 40 {
		t.Fatalf("units = %d, want 40", len(units))
	}
	counts := map[string]int{}
	owners := map[int]bool{}
	var b07 *unitSpec
	for i, u := range units {
		counts[u.SaleStatus]++
		if u.OwnerIdx >= 0 {
			owners[u.OwnerIdx] = true
		}
		if u.Code == "B07" {
			b07 = &units[i]
		}
	}
	if counts["available"] != 8 || counts["contract"] != 2 || counts["reserved"] != 1 || counts["handed_over"] != 29 {
		t.Fatalf("unexpected sale mix %v", counts)
	}
	if len(owners) != 29 || buyerReservation >= len(ownerNames) {
		t.Fatalf("owners %d, names %d", len(owners), len(ownerNames))
	}
	if b07 == nil || b07.UnitType != "3br_apartment" || b07.SaleStatus != "handed_over" {
		t.Fatalf("B07 must be a handed-over three bedroom unit: %+v", b07)
	}
	if o, r := demoReading(*b07); r-o != 9 {
		t.Fatalf("B07 consumption = %v, want 9 m3", r-o)
	}
}

func TestDemoPhoneIsFakeAndValid(t *testing.T) {
	for _, n := range []int{0, 31} {
		p := demoPhone(n)
		if !strings.HasPrefix(p, "+2547000000") || secure.NormalizePhone(p) == "" {
			t.Fatalf("phone %q is not a valid fake-range number", p)
		}
	}
	if demoPhone(0) != "+254700000001" {
		t.Fatalf("first phone = %s", demoPhone(0))
	}
}

func TestLastPeriod(t *testing.T) {
	loc := time.FixedZone("EAT", 3*3600)
	if got := lastPeriod(time.Date(2026, 1, 15, 0, 0, 0, 0, loc), loc); got != "2025-12" {
		t.Fatalf("lastPeriod = %s", got)
	}
}
