package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// DailyStat is the per-property daily aggregate dashboards read, so dashboard cost does not grow
// with raw data. Updated by event consumers, rebuilt nightly for the previous day.
type DailyStat struct{ ent.Schema }

func (DailyStat) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (DailyStat) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("property_id", uuid.UUID{}),
		field.Time("day").SchemaType(map[string]string{"postgres": "date"}),
		money("billed"),
		money("collected"),
		field.Int("invoices_issued").Default(0),
		field.Int("payments_count").Default(0),
		money("arrears_total"),
		money("arrears_0_30"),
		money("arrears_31_60"),
		money("arrears_61_90"),
		money("arrears_90_plus"),
		field.Int("units_total").Default(0),
		field.Int("units_occupied").Default(0),
		field.Int("units_vacant").Default(0),
		field.Int("units_sold").Default(0),
		qty("water_supplied_m3"),
		qty("water_billed_m3"),
		field.Int("wo_opened").Default(0),
		field.Int("wo_closed").Default(0),
		field.Int("wo_sla_breaches").Default(0),
		field.Int("visitors").Default(0),
		field.Int("incidents").Default(0),
	}
}

func (DailyStat) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "property_id", "day").Unique()}
}

// AccountCollection is what one unit account paid on one day: the per-account side of
// DailyStat.collected, so collections can be narrowed by block (through the unit) and fund the
// way billed and arrears already are. Written by the payment consumer in the same transaction as
// the daily total; older days are filled once from treasury's account ledger.
type AccountCollection struct{ ent.Schema }

func (AccountCollection) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (AccountCollection) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("unit_account_id", uuid.UUID{}),
		field.UUID("unit_id", uuid.UUID{}),
		field.UUID("property_id", uuid.UUID{}),
		field.UUID("fund_id", uuid.UUID{}),
		field.Time("day").SchemaType(map[string]string{"postgres": "date"}),
		money("amount"),
		field.Int("payments_count").Default(0),
	}
}

func (AccountCollection) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "unit_account_id", "day").Unique(),
		index.Fields("tenant_id", "property_id", "day"),
	}
}

// ImportJob tracks a CSV import from dry run to commit.
type ImportJob struct{ ent.Schema }

func (ImportJob) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (ImportJob) Fields() []ent.Field {
	return []ent.Field{
		field.Enum("kind").Values("units", "parties", "ownerships", "opening_balances", "meters", "vendors"),
		field.UUID("property_id", uuid.UUID{}).Optional().Nillable(),
		field.Bool("dry_run").Default(true),
		field.Enum("status").Values("validating", "validated", "committing", "committed", "failed").Default("validating"),
		field.String("file_name").Optional(),
		field.Int("rows_total").Default(0),
		field.Int("rows_valid").Default(0),
		field.Int("rows_failed").Default(0),
		field.Int("rows_committed").Default(0),
		field.JSON("errors", []map[string]any{}).Optional(),
		field.JSON("summary", map[string]any{}).Optional(),
		createdBy(),
	}
}

func (ImportJob) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "created_at")}
}
