// Package page implements keyset pagination on (created_at, id) descending, so list cost stays flat
// however deep the caller pages (SRDD 17.2).
package page

import (
	"net/http"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/Bengo-Hub/pagination"
	"github.com/google/uuid"
)

// Params are the parsed cursor parameters.
type Params struct {
	Limit    int
	AfterID  uuid.UUID
	AfterAt  time.Time
	HasAfter bool
}

// Parse reads ?limit and ?cursor.
func Parse(r *http.Request) Params {
	cp := pagination.ParseCursorParams(r)
	p := Params{Limit: cp.Limit}
	if cp.Cursor != "" {
		if id, at, err := pagination.DecodeCursor(cp.Cursor); err == nil {
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
