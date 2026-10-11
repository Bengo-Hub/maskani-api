package sales

import (
	"testing"
	"time"
)

func TestBeforeToday(t *testing.T) {
	loc, err := time.LoadLocation("Africa/Nairobi")
	if err != nil {
		t.Fatal(err)
	}
	day := func(s string) time.Time {
		d, err := time.ParseInLocation("2006-01-02", s, loc)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	cases := []struct {
		name string
		now  time.Time
		day  string
		want bool
	}{
		{"today in the afternoon", time.Date(2026, 10, 11, 16, 0, 0, 0, loc), "2026-10-11", false},
		{"today just after midnight", time.Date(2026, 10, 11, 0, 30, 0, 0, loc), "2026-10-11", false},
		{"today late at night", time.Date(2026, 10, 11, 23, 59, 0, 0, loc), "2026-10-11", false},
		{"yesterday", time.Date(2026, 10, 11, 9, 0, 0, 0, loc), "2026-10-10", true},
		{"tomorrow", time.Date(2026, 10, 11, 23, 59, 0, 0, loc), "2026-10-12", false},
		{"now given in UTC", time.Date(2026, 10, 10, 22, 0, 0, 0, time.UTC), "2026-10-11", false},
	}
	for _, c := range cases {
		if got := beforeToday(day(c.day), c.now, loc); got != c.want {
			t.Errorf("%s: beforeToday(%s) = %v, want %v", c.name, c.day, got, c.want)
		}
	}
}
