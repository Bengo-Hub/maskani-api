// Package sequence allocates per-tenant human-readable numbers (contracts, work orders, incidents,
// documents) from DocumentSequence, locking the counter row so concurrent allocations never
// collide. Ported from hospital-api's sequence module (itself from treasury's pattern).
package sequence

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/documentsequence"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// Default formats per kind; {yy} keeps numbers short and readable on SMS.
var defaultFormat = map[string]string{
	"sale_contract": "{prefix}-{yy}{seq}",
	"work_order":    "{prefix}-{yy}{seq}",
	"incident":      "{prefix}-{yy}{seq}",
	"document":      "{prefix}-{yy}{seq}",
}

// Allocator hands out numbers.
type Allocator struct{ client *ent.Client }

// NewAllocator creates an allocator.
func NewAllocator(client *ent.Client) *Allocator { return &Allocator{client: client} }

// Next allocates the next number for the context tenant in its own transaction.
func (a *Allocator) Next(ctx context.Context, kind, prefix string) (string, error) {
	tenantID, err := tenantguard.MustTenant(ctx)
	if err != nil {
		return "", err
	}
	tx, err := a.client.Tx(ctx)
	if err != nil {
		return "", err
	}
	n, err := next(ctx, tx, tenantID, kind, prefix)
	if err != nil {
		_ = tx.Rollback()
		return "", err
	}
	return n, tx.Commit()
}

func next(ctx context.Context, tx *ent.Tx, tenantID uuid.UUID, kind, prefix string) (string, error) {
	now := time.Now()
	seq, err := tx.DocumentSequence.Query().
		Where(documentsequence.TenantID(tenantID), documentsequence.Kind(kind)).ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		f := defaultFormat[kind]
		if f == "" {
			f = "{prefix}{seq}"
		}
		err = tx.DocumentSequence.Create().SetTenantID(tenantID).SetKind(kind).SetPrefix(prefix).
			SetNextValue(1).SetPadWidth(4).SetFormat(f).SetResetPeriod("none").
			OnConflictColumns(documentsequence.FieldTenantID, documentsequence.FieldKind).DoNothing().Exec(ctx)
		if err != nil && err.Error() != "sql: no rows in result set" {
			return "", err
		}
		seq, err = tx.DocumentSequence.Query().
			Where(documentsequence.TenantID(tenantID), documentsequence.Kind(kind)).ForUpdate().Only(ctx)
	}
	if err != nil {
		return "", err
	}
	val := seq.NextValue
	if err := tx.DocumentSequence.UpdateOne(seq).SetNextValue(val + 1).Exec(ctx); err != nil {
		return "", err
	}
	return render(seq.Format, seq.Prefix, seq.PadWidth, val, now), nil
}

func render(format, prefix string, pad int, val int64, now time.Time) string {
	if format == "" {
		format = "{prefix}{seq}"
	}
	if pad <= 0 {
		pad = 4
	}
	return strings.NewReplacer("{prefix}", prefix, "{seq}", fmt.Sprintf("%0*d", pad, val),
		"{yy}", now.Format("06"), "{yyyy}", now.Format("2006"), "{mm}", now.Format("01")).Replace(format)
}
