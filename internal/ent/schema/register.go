package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// Portfolio groups properties: one for an estate operator, one per landlord client (R2).
type Portfolio struct{ ent.Schema }

func (Portfolio) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (Portfolio) Fields() []ent.Field {
	return []ent.Field{
		field.String("code").NotEmpty(),
		field.String("name").NotEmpty(),
		field.Enum("kind").Values("own", "client").Default("own"),
		field.UUID("landlord_party_id", uuid.UUID{}).Optional().Nillable(),
		field.JSON("mandate", map[string]any{}).Optional().Comment("R2 management mandate terms until the mandates table lands"),
		field.Enum("status").Values("active", "archived").Default("active"),
		createdBy(),
	}
}

func (Portfolio) Edges() []ent.Edge {
	return []ent.Edge{edge.To("properties", Property.Type)}
}

func (Portfolio) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "code").Unique()}
}

// Property is an estate, building, plot or complex. It is also an auth-api outlet.
type Property struct{ ent.Schema }

func (Property) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (Property) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("portfolio_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("outlet_id", uuid.UUID{}).Optional().Nillable().Comment("auth-api outlet backing this property"),
		field.String("code").NotEmpty(),
		field.String("name").NotEmpty(),
		field.String("property_type").Default("estate").Comment("catalog property_type"),
		field.String("use_case").Default("estate_developer").Comment("use case preset deciding default modules"),
		field.Text("description").Optional(),
		field.String("address_line").Optional(),
		field.String("area").Optional(),
		field.String("town").Optional(),
		field.String("county").Optional(),
		field.String("country").Default("KE"),
		field.Float("latitude").Optional().Nillable(),
		field.Float("longitude").Optional().Nillable(),
		field.String("plot_number").Optional(),
		field.String("title_number").Optional(),
		optQty("land_size_sqm"),
		field.Int("year_built").Optional().Nillable(),
		field.Strings("phases").Optional(),
		field.Strings("amenities").Optional(),
		field.Strings("photos").Optional().Comment("media keys"),
		field.JSON("module_overrides", map[string]bool{}).Optional().Comment("per-property module switches over the tenant set"),
		field.Time("management_since").Optional().Nillable(),
		field.Bool("published").Default(false).Comment("visible on the marketplace showcase"),
		field.String("public_slug").Optional(),
		field.Enum("status").Values("active", "archived").Default("active"),
		customFields(),
		createdBy(),
	}
}

func (Property) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("portfolio", Portfolio.Type).Ref("properties").Field("portfolio_id").Unique(),
		edge.To("blocks", Block.Type),
		edge.To("units", Unit.Type),
	}
}

func (Property) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "code").Unique(),
		index.Fields("tenant_id", "status"),
		index.Fields("tenant_id", "outlet_id"),
		index.Fields("public_slug"),
	}
}

// Block is a building, court or phase section within a property.
type Block struct{ ent.Schema }

func (Block) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (Block) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("property_id", uuid.UUID{}),
		field.String("code").NotEmpty(),
		field.String("name").NotEmpty(),
		field.String("phase").Optional(),
		field.Int("floors").Optional().Nillable(),
		field.Int("sort").Default(0),
		field.Text("description").Optional(),
		field.Enum("status").Values("active", "archived").Default("active"),
	}
}

func (Block) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("property", Property.Type).Ref("blocks").Field("property_id").Unique().Required(),
		edge.To("units", Unit.Type),
	}
}

func (Block) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "property_id", "code").Unique()}
}

// Unit is a lettable or saleable space: house, apartment, office, shop, bay or plot.
type Unit struct{ ent.Schema }

func (Unit) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (Unit) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("property_id", uuid.UUID{}),
		field.UUID("block_id", uuid.UUID{}).Optional().Nillable(),
		field.String("code").NotEmpty().Comment("e.g. B07; also the estate paybill account reference"),
		field.String("unit_type").Default("apartment").Comment("catalog unit_type, e.g. 3br_apartment"),
		field.Enum("use").Values("residential", "office", "retail", "industrial", "parking", "land", "storage", "other").Default("residential"),
		field.Int("bedrooms").Optional().Nillable(),
		field.Int("bathrooms").Optional().Nillable(),
		optQty("size_sqm"),
		optQty("plot_size_sqm"),
		field.String("floor").Optional(),
		qty("entitlement", "share of common costs"),
		field.Int("parking_bays").Default(0),
		field.Bool("furnished").Default(false),
		field.String("phase").Optional(),
		field.Enum("sale_status").
			Values("not_for_sale", "available", "reserved", "under_agreement", "fully_paid", "handed_over", "titled", "in_default").
			Default("not_for_sale"),
		field.Enum("occupancy_status").
			Values("vacant", "owner_occupied", "tenanted", "under_renovation", "coming_vacant").
			Default("vacant"),
		field.Bool("rentable").Default(false),
		field.Time("handed_over_at").Optional().Nillable(),
		field.Strings("features").Optional(),
		field.Strings("photos").Optional(),
		field.Int("walking_order").Default(0).Comment("meter reading round order"),
		field.Enum("status").Values("active", "archived").Default("active"),
		customFields(),
		createdBy(),
	}
}

func (Unit) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("property", Property.Type).Ref("units").Field("property_id").Unique().Required(),
		edge.From("block", Block.Type).Ref("units").Field("block_id").Unique(),
		edge.To("parties", UnitParty.Type),
		edge.To("accounts", UnitAccount.Type),
	}
}

func (Unit) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "property_id", "code").Unique(),
		index.Fields("tenant_id", "property_id", "sale_status"),
		index.Fields("tenant_id", "property_id", "occupancy_status"),
		index.Fields("tenant_id", "block_id"),
	}
}

// Vehicle is a vehicle registered to a unit for gate recognition.
type Vehicle struct{ ent.Schema }

func (Vehicle) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (Vehicle) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("unit_id", uuid.UUID{}),
		field.UUID("party_id", uuid.UUID{}).Optional().Nillable(),
		field.String("plate").NotEmpty().Comment("upper case, no spaces"),
		field.String("make").Optional(),
		field.String("model").Optional(),
		field.String("colour").Optional(),
		field.String("sticker_number").Optional(),
		field.Enum("status").Values("active", "removed").Default("active"),
		field.Time("removed_at").Optional().Nillable(),
	}
}

func (Vehicle) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "plate").Unique(),
		index.Fields("tenant_id", "unit_id"),
	}
}
