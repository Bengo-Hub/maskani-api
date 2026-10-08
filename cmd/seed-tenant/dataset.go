package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// unitSpec is one demo unit of Shaba Village.
type unitSpec struct {
	Code       string
	Block      string
	UnitType   string
	Bedrooms   int
	Bathrooms  int
	SizeSqm    float64
	SaleStatus string // handed_over, available, reserved (set by the reservation), contract (set by activation)
	Occupancy  string
	OwnerIdx   int // index into owner names, -1 when unsold
	Walking    int
}

// Units A01..A20 are two bedroom (85 sqm), B01..B20 three bedroom (110 sqm). Positions 1 to 14 of
// each block and B16 are sold and handed over, A15 and B15 go under agreement through the two sale
// contracts, A16 is reserved, and A17 to A20 and B17 to B20 stay available.
func demoUnits() []unitSpec {
	var out []unitSpec
	owner := 0
	for bi, block := range []string{"A", "B"} {
		for i := 1; i <= 20; i++ {
			u := unitSpec{Code: fmt.Sprintf("%s%02d", block, i), Block: block, OwnerIdx: -1, Walking: bi*20 + i}
			if block == "A" {
				u.UnitType, u.Bedrooms, u.Bathrooms, u.SizeSqm = "2br_apartment", 2, 2, 85
			} else {
				u.UnitType, u.Bedrooms, u.Bathrooms, u.SizeSqm = "3br_apartment", 3, 2, 110
			}
			switch {
			case i <= 14 || (block == "B" && i == 16):
				u.SaleStatus, u.OwnerIdx = "handed_over", owner
				owner++
				switch {
				case i%5 == 3:
					u.Occupancy = "tenanted"
				case i%7 == 6:
					u.Occupancy = "vacant"
				default:
					u.Occupancy = "owner_occupied"
				}
			case i == 15:
				u.SaleStatus, u.Occupancy = "contract", "vacant"
			case block == "A" && i == 16:
				u.SaleStatus, u.Occupancy = "reserved", "vacant"
			default:
				u.SaleStatus, u.Occupancy = "available", "vacant"
			}
			out = append(out, u)
		}
	}
	return out
}

// ownerNames are the demo owners (29 handed-over units) and the three buyers that follow them.
var ownerNames = []string{
	"Wanjiru Kamau", "Otieno Ouma", "Achieng Odhiambo", "Mutua Musyoka", "Njeri Mwangi",
	"Kiprop Cheruiyot", "Atieno Okoth", "Mwende Kioko", "Kipchoge Rotich", "Wambui Njoroge",
	"Omondi Onyango", "Chebet Kirui", "Ndungu Gitau", "Nafula Wekesa", "Barasa Wafula",
	"Akinyi Adhiambo", "Kariuki Maina", "Jeptoo Kosgei", "Mumbua Mutiso", "Juma Hamisi",
	"Nyambura Wairimu", "Ochieng Owino", "Wanza Ndolo", "Kibet Langat", "Moraa Nyaboke",
	"Ruto Kimutai", "Zawadi Mwakio", "Gathoni Kinyua", "Makena Muriuki",
	// Buyers: the instalment contract, the milestone contract and the reservation.
	"Halima Abdi", "Kevin Mutai", "Faith Wanjala",
}

// Buyer positions in ownerNames.
const (
	buyerInstalments = 29
	buyerMilestone   = 30
	buyerReservation = 31
)

// demoPhone returns the n-th demo phone (n from 0) in an obviously fake range: +254700000001 up.
func demoPhone(n int) string { return fmt.Sprintf("+2547%08d", n+1) }

// residentUnit is the unit whose owner a real demo resident replaces: B07 is the SRDD 8.2 worked
// example (a 6,450 bill), so the resident sees a meaningful statement in the portal.
const residentUnit = "B07"

// demoResident is a real person who signs in to the owner portal during a demo.
type demoResident struct {
	Name, Phone, Email string
}

// residentFromEnv reads SEED_DEMO_RESIDENT_PHONE, _NAME and _EMAIL. The values are personal, so
// they come from the environment of one run and are never written to the repository.
func residentFromEnv() demoResident {
	r := demoResident{
		Name:  strings.TrimSpace(os.Getenv("SEED_DEMO_RESIDENT_NAME")),
		Phone: strings.TrimSpace(os.Getenv("SEED_DEMO_RESIDENT_PHONE")),
		Email: strings.ToLower(strings.TrimSpace(os.Getenv("SEED_DEMO_RESIDENT_EMAIL"))),
	}
	if r.Phone != "" && r.Name == "" {
		r.Name = "Demo Resident"
	}
	return r
}

// residentOwnerIdx is the position in ownerNames of the owner of residentUnit.
func residentOwnerIdx() int {
	for _, u := range demoUnits() {
		if u.Code == residentUnit {
			return u.OwnerIdx
		}
	}
	return -1
}

// demoReading is last month's meter reading for a unit: B07 reads 1,284 against an opening 1,275
// (9 m3, the SRDD 8.2 worked example); other occupied units use 5 to 12 m3; unsold units read zero.
func demoReading(u unitSpec) (opening, reading float64) {
	opening = 1000 + float64(u.Walking)*7
	if u.Code == "B07" {
		return 1275, 1284
	}
	if u.SaleStatus != "handed_over" || u.Occupancy == "vacant" {
		return opening, opening
	}
	return opening, opening + float64(5+u.Walking%8)
}

// lastPeriod is the month before now in loc, as YYYY-MM.
func lastPeriod(now time.Time, loc *time.Location) string {
	t := now.In(loc)
	first := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc)
	return first.AddDate(0, -1, 0).Format("2006-01")
}
