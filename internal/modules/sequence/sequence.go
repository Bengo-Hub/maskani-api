// Package sequence allocates per-tenant human-readable numbers (contracts, work orders, incidents,
// documents) from DocumentSequence, locking the counter row so concurrent allocations never
// collide. Ported from hospital-api's sequence module (itself from treasury's pattern); the
// configuration API mirrors treasury's /document-sequences.
package sequence

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/documentsequence"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// Kind is a numbered record type with its defaults.
type Kind struct {
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Prefix string `json:"default_prefix"`
	Format string `json:"default_format"`
}

// Kinds are every numbered record, in screen order. {yy} keeps numbers short on SMS and WhatsApp.
var Kinds = []Kind{
	{"sale_contract", "Sale contracts", "SC", "{prefix}-{yy}{seq}"},
	{"work_order", "Work orders", "WO", "{prefix}-{yy}{seq}"},
	{"incident", "Incidents", "INC", "{prefix}-{yy}{seq}"},
	{"document", "Documents (letters, certificates, agreements)", "DOC", "{prefix}-{yy}{seq}"},
}

func kindOf(kind string) (Kind, bool) {
	for _, k := range Kinds {
		if k.Kind == kind {
			return k, true
		}
	}
	return Kind{}, false
}

// Reset periods.
const (
	ResetNone    = "none"
	ResetYearly  = "yearly"
	ResetMonthly = "monthly"
)

// Allocator hands out numbers.
type Allocator struct {
	client *ent.Client
	loc    *time.Location
}

// NewAllocator creates an allocator; loc decides when a year or month turns for resets and dates.
func NewAllocator(client *ent.Client, loc *time.Location) *Allocator {
	if loc == nil {
		loc = time.UTC
	}
	return &Allocator{client: client, loc: loc}
}

// Next allocates the next number for the context tenant in its own transaction. prefix is used
// only when the tenant has no sequence for the kind yet.
func (a *Allocator) Next(ctx context.Context, kind, prefix string) (string, error) {
	tenantID, err := tenantguard.MustTenant(ctx)
	if err != nil {
		return "", err
	}
	tx, err := a.client.Tx(ctx)
	if err != nil {
		return "", err
	}
	n, err := a.next(ctx, tx, tenantID, kind, prefix)
	if err != nil {
		_ = tx.Rollback()
		return "", err
	}
	return n, tx.Commit()
}

func (a *Allocator) next(ctx context.Context, tx *ent.Tx, tenantID uuid.UUID, kind, prefix string) (string, error) {
	now := time.Now().In(a.loc)
	seq, err := a.lockOrCreate(ctx, tx, tenantID, kind, prefix)
	if err != nil {
		return "", err
	}
	val := seq.NextValue
	upd := tx.DocumentSequence.UpdateOne(seq)
	// A new year or month on a resetting series starts again at 1.
	if key := periodKey(seq.ResetPeriod, now); key != "" && key != seq.PeriodKey {
		val = 1
		upd.SetPeriodKey(key)
	}
	if err := upd.SetNextValue(val + 1).Exec(ctx); err != nil {
		return "", err
	}
	return render(seq.Format, seq.Prefix, seq.PadWidth, val, now), nil
}

// lockOrCreate returns the kind's counter row locked for update, creating it with the defaults.
func (a *Allocator) lockOrCreate(ctx context.Context, tx *ent.Tx, tenantID uuid.UUID, kind, prefix string) (*ent.DocumentSequence, error) {
	q := func() (*ent.DocumentSequence, error) {
		return tx.DocumentSequence.Query().
			Where(documentsequence.TenantID(tenantID), documentsequence.Kind(kind)).ForUpdate().Only(ctx)
	}
	seq, err := q()
	if !ent.IsNotFound(err) {
		return seq, err
	}
	format := "{prefix}{seq}"
	if k, ok := kindOf(kind); ok {
		format = k.Format
		if prefix == "" {
			prefix = k.Prefix
		}
	}
	err = tx.DocumentSequence.Create().SetTenantID(tenantID).SetKind(kind).SetPrefix(prefix).
		SetNextValue(1).SetPadWidth(4).SetFormat(format).SetResetPeriod(ResetNone).
		OnConflictColumns(documentsequence.FieldTenantID, documentsequence.FieldKind).DoNothing().Exec(ctx)
	if err != nil && err.Error() != "sql: no rows in result set" {
		return nil, err
	}
	return q()
}

func periodKey(reset string, now time.Time) string {
	switch reset {
	case ResetYearly:
		return now.Format("2006")
	case ResetMonthly:
		return now.Format("2006-01")
	}
	return ""
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

// Config is one kind's numbering as the settings screen shows it.
type Config struct {
	Kind        string `json:"kind"`
	Label       string `json:"label"`
	Prefix      string `json:"prefix"`
	Format      string `json:"format"`
	PadWidth    int    `json:"pad_width"`
	ResetPeriod string `json:"reset_period"`
	NextValue   int64  `json:"next_value"`
	NextNumber  string `json:"next_number"`
}

// Configs lists every kind, unsaved ones with their defaults.
func (a *Allocator) Configs(ctx context.Context) ([]Config, error) {
	tenantID, err := tenantguard.MustTenant(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := a.client.DocumentSequence.Query().Where(documentsequence.TenantID(tenantID)).All(ctx)
	if err != nil {
		return nil, err
	}
	byKind := map[string]*ent.DocumentSequence{}
	for _, r := range rows {
		byKind[r.Kind] = r
	}
	now := time.Now().In(a.loc)
	out := make([]Config, 0, len(Kinds))
	for _, k := range Kinds {
		c := Config{Kind: k.Kind, Label: k.Label, Prefix: k.Prefix, Format: k.Format, PadWidth: 4, ResetPeriod: ResetNone, NextValue: 1}
		if r := byKind[k.Kind]; r != nil {
			c.Prefix, c.Format, c.PadWidth, c.ResetPeriod, c.NextValue = r.Prefix, r.Format, r.PadWidth, r.ResetPeriod, r.NextValue
			if key := periodKey(r.ResetPeriod, now); key != "" && key != r.PeriodKey {
				c.NextValue = 1
			}
		}
		c.NextNumber = render(c.Format, c.Prefix, c.PadWidth, c.NextValue, now)
		out = append(out, c)
	}
	return out, nil
}

// Update is a change to a kind's numbering. NextValue, when set, continues an existing series
// (the next number issued uses it).
type Update struct {
	Prefix      *string `json:"prefix"`
	Format      *string `json:"format"`
	PadWidth    *int    `json:"pad_width"`
	ResetPeriod *string `json:"reset_period"`
	NextValue   *int64  `json:"next_value"`
}

var prefixRe = regexp.MustCompile(`^[A-Za-z0-9/-]{0,12}$`)

// Save validates and applies a change, creating the row when the kind has none yet.
func (a *Allocator) Save(ctx context.Context, kind string, in Update) (*Config, error) {
	k, ok := kindOf(kind)
	if !ok {
		return nil, httpx.Invalid("unknown kind")
	}
	tenantID, err := tenantguard.MustTenant(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := a.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	seq, err := a.lockOrCreate(ctx, tx, tenantID, kind, k.Prefix)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	prefix, format, pad, reset := seq.Prefix, seq.Format, seq.PadWidth, seq.ResetPeriod
	if in.Prefix != nil {
		prefix = strings.TrimSpace(*in.Prefix)
	}
	if in.Format != nil {
		format = strings.TrimSpace(*in.Format)
	}
	if in.PadWidth != nil {
		pad = *in.PadWidth
	}
	if in.ResetPeriod != nil {
		reset = *in.ResetPeriod
	}
	if err := validate(prefix, format, pad, reset); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	upd := tx.DocumentSequence.UpdateOne(seq).SetPrefix(prefix).SetFormat(format).SetPadWidth(pad).SetResetPeriod(reset).
		SetPeriodKey(periodKey(reset, time.Now().In(a.loc)))
	if in.NextValue != nil {
		if *in.NextValue < 1 {
			_ = tx.Rollback()
			return nil, httpx.Invalid("the next number must be 1 or more")
		}
		upd.SetNextValue(*in.NextValue)
	}
	if err := upd.Exec(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	all, err := a.Configs(ctx)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].Kind == kind {
			return &all[i], nil
		}
	}
	return nil, nil
}

// validate keeps a series unique: a series that restarts each year or month must show that year
// or month, or its numbers would repeat.
func validate(prefix, format string, pad int, reset string) error {
	if !prefixRe.MatchString(prefix) {
		return httpx.Invalid("the prefix may hold up to 12 letters, digits, dashes or slashes")
	}
	if !strings.Contains(format, "{seq}") {
		return httpx.Invalid("the format must contain {seq}")
	}
	if pad < 1 || pad > 10 {
		return httpx.Invalid("the number width must be between 1 and 10")
	}
	hasYear := strings.Contains(format, "{yy}") || strings.Contains(format, "{yyyy}")
	switch reset {
	case ResetNone:
	case ResetYearly:
		if !hasYear {
			return httpx.Invalid("a series that restarts every year must include {yy} or {yyyy}, or its numbers would repeat")
		}
	case ResetMonthly:
		if !hasYear || !strings.Contains(format, "{mm}") {
			return httpx.Invalid("a series that restarts every month must include the year and {mm}, or its numbers would repeat")
		}
	default:
		return httpx.Invalid("restart must be none, yearly or monthly")
	}
	return nil
}
