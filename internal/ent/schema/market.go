package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// Enquiry is a marketplace enquiry about a unit or estate. Added with the R1 estate showcase and
// shaped for R3 (listing_id, viewing and CRM lead links arrive through metadata and later columns).
// The seeker's phone is stored encrypted; staff see it only after the seeker agrees to share.
type Enquiry struct{ ent.Schema }

func (Enquiry) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (Enquiry) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("property_id", uuid.UUID{}),
		field.UUID("unit_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("listing_id", uuid.UUID{}).Optional().Nillable(),
		field.String("name").NotEmpty(),
		field.String("phone_enc").Optional().Sensitive(),
		field.String("phone_hash").Optional(),
		field.String("email").Optional(),
		field.Text("message").Optional(),
		field.Enum("interest").Values("buy", "rent", "info").Default("buy"),
		field.Enum("preferred_contact").Values("call", "sms", "whatsapp", "email").Default("call"),
		field.Bool("consent_to_share").Default(false),
		field.Enum("source").Values("showcase", "marketplace", "portal", "walk_in").Default("showcase"),
		field.Enum("status").Values("new", "contacted", "viewing_booked", "converted", "closed", "spam").Default("new"),
		field.UUID("assigned_to", uuid.UUID{}).Optional().Nillable(),
		field.String("crm_lead_id").Optional(),
		field.String("ip_hash").Optional(),
	}
}

func (Enquiry) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "status", "created_at"),
		index.Fields("tenant_id", "property_id"),
		index.Fields("tenant_id", "phone_hash"),
		index.Fields("tenant_id", "property_id", "created_at", "id"),
		index.Fields("tenant_id", "created_at", "id"),
	}
}
