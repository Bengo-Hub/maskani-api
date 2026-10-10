package settings

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/bengobox/maskani-api/internal/ent"
)

// ArrearsStep is one rung of the collections ladder: when an account's oldest unpaid bill is Day
// days past due, Action happens once for that debt.
type ArrearsStep struct {
	Day    int    `json:"day"`
	Action string `json:"action"` // reminder, call_list, demand_letter, escalate
}

// Arrears actions.
const (
	ArrearsReminder     = "reminder"
	ArrearsCallList     = "call_list"
	ArrearsDemandLetter = "demand_letter"
	ArrearsEscalate     = "escalate"
)

// DefaultArrearsSteps is the ladder an estate starts with (SRDD collections ladder).
var DefaultArrearsSteps = []ArrearsStep{
	{1, ArrearsReminder}, {7, ArrearsReminder}, {14, ArrearsReminder},
	{30, ArrearsCallList}, {45, ArrearsDemandLetter}, {60, ArrearsEscalate},
}

// ArrearsSteps reads an estate's ladder in day order, or the default when none is set.
func ArrearsSteps(st *ent.TenantSetting) []ArrearsStep {
	if st == nil || len(st.ArrearsSteps) == 0 {
		return DefaultArrearsSteps
	}
	raw, _ := json.Marshal(st.ArrearsSteps)
	var out []ArrearsStep
	if json.Unmarshal(raw, &out) != nil || validateArrears(out) != nil {
		return DefaultArrearsSteps
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day < out[j].Day })
	return out
}

func validateArrears(steps []ArrearsStep) error {
	if len(steps) > 12 {
		return fmt.Errorf("at most 12 collection steps")
	}
	seen := map[int]bool{}
	for _, s := range steps {
		if s.Day < 1 || s.Day > 365 {
			return fmt.Errorf("collection step days must be between 1 and 365")
		}
		if seen[s.Day] {
			return fmt.Errorf("two collection steps on day %d", s.Day)
		}
		seen[s.Day] = true
		switch s.Action {
		case ArrearsReminder, ArrearsCallList, ArrearsDemandLetter, ArrearsEscalate:
		default:
			return fmt.Errorf("unknown collection action %q", s.Action)
		}
	}
	return nil
}

func arrearsJSON(steps []ArrearsStep) []map[string]any {
	out := make([]map[string]any, len(steps))
	for i, s := range steps {
		out[i] = map[string]any{"day": s.Day, "action": s.Action}
	}
	return out
}
