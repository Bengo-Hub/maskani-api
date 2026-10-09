// Package page implements keyset pagination on (created_at, id) descending, so list cost stays flat
// however deep the caller pages (SRDD 17.2). Lists sorted by another column (arrears by balance)
// use the keyed cursor below with the same response envelope.
package page

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/Bengo-Hub/pagination"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// MaxLimit is the largest page; asking for more returns MaxLimit rows (not the default).
const MaxLimit = 100

// Params are the parsed cursor parameters.
type Params struct {
	Limit    int
	AfterID  uuid.UUID
	AfterAt  time.Time
	HasAfter bool
}

// Limit reads ?limit: missing or invalid gives the shared default (20), above MaxLimit gives MaxLimit.
func Limit(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	switch {
	case err != nil || n <= 0:
		return pagination.DefaultLimit
	case n > MaxLimit:
		return MaxLimit
	}
	return n
}

// Parse reads ?limit and ?cursor.
func Parse(r *http.Request) Params {
	p := Params{Limit: Limit(r)}
	if c := r.URL.Query().Get("cursor"); c != "" {
		if id, at, err := pagination.DecodeCursor(c); err == nil {
			p.AfterID, p.AfterAt, p.HasAfter = id, at, true
		}
	}
	return p
}

// Predicate restricts a query to rows after the cursor (created_at DESC, id DESC).
func (p Params) Predicate() func(*sql.Selector) {
	return func(s *sql.Selector) {
		if !p.HasAfter {
			return
		}
		s.Where(sql.Or(
			sql.LT(s.C("created_at"), p.AfterAt),
			sql.And(sql.EQ(s.C("created_at"), p.AfterAt), sql.LT(s.C("id"), p.AfterID)),
		))
	}
}

// Order is the matching sort.
func Order() func(*sql.Selector) {
	return func(s *sql.Selector) {
		s.OrderBy(sql.Desc(s.C("created_at")), sql.Desc(s.C("id")))
	}
}

// Result is the response envelope.
type Result[T any] struct {
	Data       []T    `json:"data"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// Build trims the extra row fetched to detect more pages and builds the next cursor.
func Build[T any](rows []T, limit int, key func(T) (uuid.UUID, time.Time)) Result[T] {
	res := Result[T]{Data: rows}
	if len(rows) > limit {
		res.Data = rows[:limit]
		res.HasMore = true
		id, at := key(res.Data[len(res.Data)-1])
		res.NextCursor = pagination.EncodeCursor(id, at)
	}
	if res.Data == nil {
		res.Data = []T{}
	}
	return res
}

// DecimalParams page a list sorted by a decimal column descending, then id descending.
type DecimalParams struct {
	Limit    int
	AfterID  uuid.UUID
	AfterVal decimal.Decimal
	HasAfter bool
}

// ParseDecimal reads ?limit and a cursor made by BuildDecimal.
func ParseDecimal(r *http.Request) DecimalParams {
	p := DecimalParams{Limit: Limit(r)}
	if c := r.URL.Query().Get("cursor"); c != "" {
		if id, v, err := decodeDecimal(c); err == nil {
			p.AfterID, p.AfterVal, p.HasAfter = id, v, true
		}
	}
	return p
}

// Predicate restricts to rows after the cursor on (column DESC, id DESC).
func (p DecimalParams) Predicate(column string) func(*sql.Selector) {
	return func(s *sql.Selector) {
		if !p.HasAfter {
			return
		}
		s.Where(sql.Or(
			sql.LT(s.C(column), p.AfterVal),
			sql.And(sql.EQ(s.C(column), p.AfterVal), sql.LT(s.C("id"), p.AfterID)),
		))
	}
}

// OrderDecimal sorts by column DESC, id DESC.
func OrderDecimal(column string) func(*sql.Selector) {
	return func(s *sql.Selector) {
		s.OrderBy(sql.Desc(s.C(column)), sql.Desc(s.C("id")))
	}
}

// BuildDecimal trims the probe row and encodes the next cursor from the last row's value and id.
func BuildDecimal[T any](rows []T, limit int, key func(T) (uuid.UUID, decimal.Decimal)) Result[T] {
	return buildKeyed(rows, limit, func(row T) (uuid.UUID, string) {
		id, v := key(row)
		return id, v.String()
	})
}

func decodeDecimal(c string) (uuid.UUID, decimal.Decimal, error) {
	id, s, err := decodeKeyed(c)
	if err != nil {
		return uuid.Nil, decimal.Zero, err
	}
	v, err := decimal.NewFromString(s)
	return id, v, err
}

// TextParams page a list sorted by a text column ascending, then id ascending (unit codes on a
// billing run, for example).
type TextParams struct {
	Limit    int
	AfterID  uuid.UUID
	AfterVal string
	HasAfter bool
}

// ParseText reads ?limit and a cursor made by BuildText.
func ParseText(r *http.Request) TextParams {
	p := TextParams{Limit: Limit(r)}
	if c := r.URL.Query().Get("cursor"); c != "" {
		if id, v, err := decodeKeyed(c); err == nil {
			p.AfterID, p.AfterVal, p.HasAfter = id, v, true
		}
	}
	return p
}

// Predicate restricts to rows after the cursor on (column ASC, id ASC).
func (p TextParams) Predicate(column string) func(*sql.Selector) {
	return func(s *sql.Selector) {
		if !p.HasAfter {
			return
		}
		s.Where(sql.Or(
			sql.GT(s.C(column), p.AfterVal),
			sql.And(sql.EQ(s.C(column), p.AfterVal), sql.GT(s.C("id"), p.AfterID)),
		))
	}
}

// OrderText sorts by column ASC, id ASC.
func OrderText(column string) func(*sql.Selector) {
	return func(s *sql.Selector) {
		s.OrderBy(sql.Asc(s.C(column)), sql.Asc(s.C("id")))
	}
}

// BuildText trims the probe row and encodes the next cursor from the last row's value and id.
func BuildText[T any](rows []T, limit int, key func(T) (uuid.UUID, string)) Result[T] {
	return buildKeyed(rows, limit, key)
}

// buildKeyed is the shared envelope for cursors keyed on (value, id).
func buildKeyed[T any](rows []T, limit int, key func(T) (uuid.UUID, string)) Result[T] {
	res := Result[T]{Data: rows}
	if len(rows) > limit {
		res.Data = rows[:limit]
		res.HasMore = true
		id, v := key(res.Data[len(res.Data)-1])
		res.NextCursor = base64.URLEncoding.EncodeToString([]byte(id.String() + "|" + v))
	}
	if res.Data == nil {
		res.Data = []T{}
	}
	return res
}

func decodeKeyed(c string) (uuid.UUID, string, error) {
	raw, err := base64.URLEncoding.DecodeString(c)
	if err != nil {
		return uuid.Nil, "", err
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return uuid.Nil, "", strconv.ErrSyntax
	}
	id, err := uuid.Parse(parts[0])
	if err != nil {
		return uuid.Nil, "", err
	}
	return id, parts[1], nil
}
