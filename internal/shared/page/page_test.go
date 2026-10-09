package page

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestLimitCapsAtMax(t *testing.T) {
	for q, want := range map[string]int{"": 20, "?limit=0": 20, "?limit=abc": 20, "?limit=50": 50, "?limit=100": 100, "?limit=500": 100} {
		if got := Limit(httptest.NewRequest("GET", "/x"+q, nil)); got != want {
			t.Fatalf("limit for %q = %d, want %d", q, got, want)
		}
	}
}

func TestCursorRoundTrip(t *testing.T) {
	type row struct {
		id uuid.UUID
		at time.Time
	}
	rows := []row{{uuid.New(), time.Now()}, {uuid.New(), time.Now().Add(-time.Minute)}, {uuid.New(), time.Now().Add(-2 * time.Minute)}}
	res := Build(rows, 2, func(r row) (uuid.UUID, time.Time) { return r.id, r.at })
	if !res.HasMore || len(res.Data) != 2 || res.NextCursor == "" {
		t.Fatalf("unexpected page %+v", res)
	}
	p := Parse(httptest.NewRequest("GET", "/x?cursor="+res.NextCursor, nil))
	if !p.HasAfter || p.AfterID != rows[1].id {
		t.Fatalf("cursor did not round trip: %+v", p)
	}
}

func TestDecimalCursorRoundTrip(t *testing.T) {
	type row struct {
		id  uuid.UUID
		bal decimal.Decimal
	}
	rows := []row{{uuid.New(), decimal.RequireFromString("9000.50")}, {uuid.New(), decimal.RequireFromString("6450.00")}}
	res := BuildDecimal(rows, 1, func(r row) (uuid.UUID, decimal.Decimal) { return r.id, r.bal })
	p := ParseDecimal(httptest.NewRequest("GET", "/x?cursor="+res.NextCursor, nil))
	if !p.HasAfter || p.AfterID != rows[0].id || !p.AfterVal.Equal(rows[0].bal) {
		t.Fatalf("decimal cursor did not round trip: %+v", p)
	}
	if empty := BuildDecimal([]row(nil), 5, func(r row) (uuid.UUID, decimal.Decimal) { return r.id, r.bal }); empty.Data == nil || empty.HasMore {
		t.Fatal("empty page must be [] with has_more false")
	}
}

func TestTextCursorRoundTrip(t *testing.T) {
	type row struct {
		id   uuid.UUID
		code string
	}
	// A code may hold the separator; only the first "|" splits the cursor.
	rows := []row{{uuid.New(), "A|1"}, {uuid.New(), "A2"}}
	res := BuildText(rows, 1, func(r row) (uuid.UUID, string) { return r.id, r.code })
	p := ParseText(httptest.NewRequest("GET", "/x?cursor="+res.NextCursor, nil))
	if !p.HasAfter || p.AfterID != rows[0].id || p.AfterVal != "A|1" {
		t.Fatalf("text cursor did not round trip: %+v", p)
	}
}
