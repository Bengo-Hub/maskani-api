package settings

import (
	"encoding/json"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/bengobox/maskani-api/internal/ent"
)

// LateCharge is an estate's late payment charge, kept in settings metadata ("late_charge"). Off
// unless the estate's rules provide for it. Once a month, from Day, each account in Funds with
// bills more than GraceDays past due is charged Percent of that overdue amount plus Fixed, at most
// Cap. Earlier late charges never count toward the overdue amount, so it never compounds.
type LateCharge struct {
	Enabled   bool            `json:"enabled"`
	Percent   decimal.Decimal `json:"percent"`
	Fixed     decimal.Decimal `json:"fixed"`
	Cap       decimal.Decimal `json:"cap"`
	GraceDays int             `json:"grace_days"`
	Day       int             `json:"day"`
	Funds     []string        `json:"funds"`
}

// DefaultLateCharge is off, with the values an estate usually starts from when it switches it on.
var DefaultLateCharge = LateCharge{Percent: decimal.NewFromInt(2), GraceDays: 30, Day: 1, Funds: []string{"estate"}}

// LateChargeOf reads the estate's late charge, or the default (off) when unset or unreadable.
func LateChargeOf(st *ent.TenantSetting) LateCharge {
	if st == nil || st.Metadata["late_charge"] == nil {
		return DefaultLateCharge
	}
	raw, _ := json.Marshal(st.Metadata["late_charge"])
	var lc LateCharge
	if json.Unmarshal(raw, &lc) != nil || validateLateCharge(lc) != nil {
		return DefaultLateCharge
	}
	return lc
}

func validateLateCharge(lc LateCharge) error {
	switch {
	case lc.Percent.IsNegative() || lc.Percent.GreaterThan(decimal.NewFromInt(10)):
		return fmt.Errorf("the late charge percent must be between 0 and 10 a month")
	case lc.Fixed.IsNegative() || lc.Cap.IsNegative():
		return fmt.Errorf("late charge amounts cannot be negative")
	case lc.Enabled && lc.Percent.IsZero() && lc.Fixed.IsZero():
		return fmt.Errorf("set a percent or a fixed amount for the late charge")
	case lc.GraceDays < 0 || lc.GraceDays > 180:
		return fmt.Errorf("late charge grace days must be between 0 and 180")
	case lc.Day < 1 || lc.Day > 28:
		return fmt.Errorf("the late charge day must be between 1 and 28")
	case lc.Enabled && len(lc.Funds) == 0:
		return fmt.Errorf("choose at least one fund for the late charge")
	}
	return nil
}

// Amount works out one month's charge on an overdue amount: percent plus fixed, rounded to the
// cent, at most the cap (when set). Nothing is owed on nothing overdue.
func (lc LateCharge) Amount(overdue decimal.Decimal) decimal.Decimal {
	if !overdue.IsPositive() {
		return decimal.Zero
	}
	amt := overdue.Mul(lc.Percent).Div(decimal.NewFromInt(100)).Add(lc.Fixed).Round(2)
	if lc.Cap.IsPositive() && amt.GreaterThan(lc.Cap) {
		amt = lc.Cap
	}
	return amt
}

func lateChargeJSON(lc LateCharge) map[string]any {
	return map[string]any{"enabled": lc.Enabled, "percent": lc.Percent.String(), "fixed": lc.Fixed.String(),
		"cap": lc.Cap.String(), "grace_days": lc.GraceDays, "day": lc.Day, "funds": lc.Funds}
}
