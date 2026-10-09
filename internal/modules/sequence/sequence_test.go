package sequence

import (
	"testing"
	"time"
)

func TestRender(t *testing.T) {
	now := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	if got := render("{prefix}-{yy}{seq}", "WO", 4, 7, now); got != "WO-260007" {
		t.Fatalf("render = %q", got)
	}
	if got := render("{prefix}/{yyyy}/{mm}/{seq}", "DOC", 3, 12, now); got != "DOC/2026/10/012" {
		t.Fatalf("render = %q", got)
	}
}

func TestPeriodKey(t *testing.T) {
	now := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	if periodKey(ResetNone, now) != "" || periodKey(ResetYearly, now) != "2026" || periodKey(ResetMonthly, now) != "2026-10" {
		t.Fatal("period keys are wrong")
	}
}

// A restarting series must carry its period in the number, or numbers would repeat.
func TestValidate(t *testing.T) {
	cases := []struct {
		prefix, format, reset string
		pad                   int
		ok                    bool
	}{
		{"WO", "{prefix}-{yy}{seq}", ResetNone, 4, true},
		{"WO", "{prefix}-{yy}{seq}", ResetYearly, 4, true},
		{"WO", "{prefix}-{seq}", ResetYearly, 4, false},
		{"WO", "{prefix}-{yy}{seq}", ResetMonthly, 4, false},
		{"WO", "{prefix}-{yyyy}{mm}{seq}", ResetMonthly, 4, true},
		{"WO", "{prefix}", ResetNone, 4, false},
		{"W O", "{seq}", ResetNone, 4, false},
		{"WO", "{seq}", ResetNone, 0, false},
		{"WO", "{seq}", "weekly", 4, false},
	}
	for _, c := range cases {
		if err := validate(c.prefix, c.format, c.pad, c.reset); (err == nil) != c.ok {
			t.Errorf("validate(%q, %q, %d, %q) = %v, want ok %v", c.prefix, c.format, c.pad, c.reset, err, c.ok)
		}
	}
}
