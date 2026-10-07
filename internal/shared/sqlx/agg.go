// Package sqlx holds aliased aggregate helpers. Ent's bare Sum/Count emit unaliased columns that
// collide when a query has more than one, so every report aggregate goes through these.
package sqlx

import (
	"fmt"

	"entgo.io/ent/dialect/sql"

	"github.com/bengobox/maskani-api/internal/ent"
)

// CountAs is COUNT(*) [FILTER (WHERE filter)] AS alias. filter is a trusted SQL literal written in
// code, never user input.
func CountAs(alias, filter string) ent.AggregateFunc {
	return func(s *sql.Selector) string {
		if filter == "" {
			return sql.As("COUNT(*)", alias)
		}
		return sql.As(fmt.Sprintf("COUNT(*) FILTER (WHERE %s)", filter), alias)
	}
}

// SumAs is COALESCE(SUM(column) [FILTER (WHERE filter)], 0) AS alias.
func SumAs(column, alias, filter string) ent.AggregateFunc {
	return func(s *sql.Selector) string {
		expr := fmt.Sprintf("SUM(%s)", s.C(column))
		if filter != "" {
			expr = fmt.Sprintf("SUM(%s) FILTER (WHERE %s)", s.C(column), filter)
		}
		return sql.As(fmt.Sprintf("COALESCE(%s, 0)", expr), alias)
	}
}
