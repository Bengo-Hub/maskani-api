package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// Party is a person or company the tenant deals with: owner, buyer, occupant, landlord, guarantor.
// The same person can be a party at several tenants; each tenant sees only its own record.
type Party struct{ ent.Schema }

func (Party) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (Party) Fields() []ent.Field {
	return []ent.Field{
		field.Enum("kind").Values("person", "company").Default("person"),
		field.String("display_name").NotEmpty(),
		field.String("first_name").Optional(),
		field.String("last_name").Optional(),
		field.String("company_name").Optional(),
		field.String("registration_number").Optional(),
		field.String("phone").Optional().Comment("E.164 without plus, e.g. 254712345678"),
		field.String("phone_hash").Optional().Comment("HMAC of the normalised phone, for matching"),
		field.String("alt_phone").Optional(),
		field.String("email").Optional(),
		field.Enum("id_type").Values("national_id", "passport", "alien_id", "company_reg", "none").Default("none"),
		field.String("national_id_enc").Optional().Sensitive().Comment("AES-GCM encrypted"),
		field.String("kra_pin_enc").Optional().Sensitive().Comment("AES-GCM encrypted"),
		field.String("nationality").Optional(),
		field.Bool("is_tax_resident").Default(true),
		field.Bool("is_diaspora").Default(false),
		field.String("postal_address").Optional(),
		field.UUID("auth_user_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("crm_contact_id", uuid.UUID{}).Optional().Nillable(),
		field.Enum("preferred_channel").Values("sms", "whatsapp", "email").Default("sms"),
		field.String("language").Default("en"),
		field.JSON("consents", map[string]any{}).Optional(),
		field.String("terms_accepted_version").Optional(),
		field.Time("terms_accepted_at").Optional().Nillable(),
		field.Time("invited_at").Optional().Nillable(),
		field.Enum("status").Values("active", "inactive", "anonymised").Default("active"),
		field.Text("notes").Optional(),
		customFields(),
		createdBy(),
	}
}

func (Party) Edges() []ent.Edge {
	return []ent.Edge{edge.To("unit_links", UnitParty.Type)}
}

func (Party) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "phone_hash"),
		index.Fields("tenant_id", "auth_user_id"),
		index.Fields("tenant_id", "display_name"),
	}
}

// UnitParty is a dated relationship between a unit and a party: ownership, purchase or occupancy.
type UnitParty struct{ ent.Schema }

func (UnitParty) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (UnitParty) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("unit_id", uuid.UUID{}),
		field.UUID("party_id", uuid.UUID{}),
		field.Enum("role").Values("owner", "joint_owner", "buyer", "occupant", "household_member", "domestic_staff", "emergency_contact", "landlord"),
		qty("ownership_share", "percentage for joint owners"),
		field.Bool("is_primary").Default(false),
		field.Time("start_date"),
		field.Time("end_date").Optional().Nillable(),
		field.Strings("bill_to").Optional().Comment("charge type codes this party receives (assigned by the owner)"),
		field.Int("revert_after_days").Optional().Nillable(),
		field.Enum("source").Values("import", "manual", "sale", "transfer", "portal").Default("manual"),
		field.Enum("status").Values("active", "ended").Default("active"),
		createdBy(),
	}
}

func (UnitParty) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("unit", Unit.Type).Ref("parties").Field("unit_id").Unique().Required(),
		edge.From("party", Party.Type).Ref("unit_links").Field("party_id").Unique().Required(),
	}
}

func (UnitParty) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "unit_id", "role", "status"),
		index.Fields("tenant_id", "party_id", "status"),
	}
}
