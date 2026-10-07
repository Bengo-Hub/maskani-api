package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// Meter is a unit sub-meter or a bulk, borehole or common-area meter.
type Meter struct{ ent.Schema }

func (Meter) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (Meter) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("property_id", uuid.UUID{}),
		field.UUID("unit_id", uuid.UUID{}).Optional().Nillable(),
		field.Enum("kind").Values("unit", "bulk_supply", "borehole", "common_area").Default("unit"),
		field.Enum("utility").Values("water", "gas", "electricity_info").Default("water"),
		field.String("serial").NotEmpty(),
		field.String("make").Optional(),
		field.String("location_note").Optional(),
		field.Time("installed_at").Optional().Nillable(),
		qty("multiplier"),
		qty("initial_reading"),
		optQty("closing_reading"),
		field.Int("walking_order").Default(0),
		field.Enum("status").Values("active", "replaced", "faulty", "removed").Default("active"),
		field.UUID("replaced_by_id", uuid.UUID{}).Optional().Nillable(),
	}
}

func (Meter) Edges() []ent.Edge {
	return []ent.Edge{edge.To("readings", MeterReading.Type)}
}

func (Meter) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "serial").Unique(),
		index.Fields("tenant_id", "property_id", "kind", "status"),
		index.Fields("tenant_id", "unit_id"),
	}
}

// ReadingRound is a property's reading round for one period.
type ReadingRound struct{ ent.Schema }

func (ReadingRound) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (ReadingRound) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("property_id", uuid.UUID{}),
		field.String("period").NotEmpty().Comment("YYYY-MM"),
		field.Enum("status").Values("open", "validating", "closed").Default("open"),
		field.UUID("assigned_to", uuid.UUID{}).Optional().Nillable(),
		field.Time("opened_at").Optional().Nillable(),
		field.Time("closed_at").Optional().Nillable(),
		field.JSON("totals", map[string]any{}).Optional(),
	}
}

func (ReadingRound) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "property_id", "period").Unique()}
}

// MeterReading is one reading with photo evidence, anomaly flags and estimation state.
type MeterReading struct{ ent.Schema }

func (MeterReading) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (MeterReading) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("round_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("meter_id", uuid.UUID{}),
		field.UUID("unit_id", uuid.UUID{}).Optional().Nillable(),
		field.String("period").NotEmpty(),
		qty("reading"),
		optQty("previous_reading"),
		qty("consumption"),
		field.Time("read_at"),
		field.UUID("read_by", uuid.UUID{}).Optional().Nillable(),
		field.String("photo_key").Optional(),
		field.Enum("source").Values("round", "estimate", "import", "handover", "move_out", "replacement").Default("round"),
		field.Bool("is_estimated").Default(false),
		field.Strings("flags").Optional().Comment("lower_than_previous, zero_occupied, spike"),
		field.Enum("status").Values("pending", "accepted", "recheck", "rejected").Default("pending"),
		field.UUID("verified_by", uuid.UUID{}).Optional().Nillable(),
		field.Text("notes").Optional(),
	}
}

func (MeterReading) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("meter", Meter.Type).Ref("readings").Field("meter_id").Unique().Required(),
	}
}

func (MeterReading) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "meter_id", "period", "source").Unique(),
		index.Fields("tenant_id", "round_id", "status"),
		index.Fields("tenant_id", "unit_id", "period"),
	}
}
