package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// Notice is a message to an audience (estate, blocks, units, owners, occupants) over channels.
type Notice struct{ ent.Schema }

func (Notice) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (Notice) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("property_id", uuid.UUID{}).Optional().Nillable(),
		field.JSON("audience", map[string]any{}).Comment(`{"scope":"estate|blocks|units","block_ids":[],"unit_ids":[],"roles":["owner","occupant"]}`),
		field.Strings("channels"),
		field.String("category").Default("general"),
		field.Enum("priority").Values("routine", "emergency").Default("routine"),
		field.String("title").NotEmpty(),
		field.Text("body"),
		field.Time("scheduled_at").Optional().Nillable(),
		field.Time("sent_at").Optional().Nillable(),
		field.Enum("status").Values("draft", "scheduled", "sending", "sent", "failed").Default("draft"),
		field.Int("recipients_count").Default(0),
		field.Int("delivered_count").Default(0),
		createdBy(),
	}
}

func (Notice) Edges() []ent.Edge {
	return []ent.Edge{edge.To("deliveries", NoticeDelivery.Type)}
}

func (Notice) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "status", "scheduled_at"), index.Fields("tenant_id", "created_at")}
}

// NoticeDelivery tracks one recipient and channel.
type NoticeDelivery struct{ ent.Schema }

func (NoticeDelivery) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (NoticeDelivery) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("notice_id", uuid.UUID{}),
		field.UUID("party_id", uuid.UUID{}),
		field.String("channel").NotEmpty(),
		field.String("destination").Optional(),
		field.String("notification_message_id").Optional(),
		field.Enum("status").Values("queued", "sent", "delivered", "failed").Default("queued"),
		field.Text("error").Optional(),
		field.Time("sent_at").Optional().Nillable(),
	}
}

func (NoticeDelivery) Edges() []ent.Edge {
	return []ent.Edge{edge.From("notice", Notice.Type).Ref("deliveries").Field("notice_id").Unique().Required()}
}

func (NoticeDelivery) Indexes() []ent.Index {
	return []ent.Index{index.Fields("notice_id", "party_id", "channel").Unique(), index.Fields("tenant_id", "status")}
}

// DocumentTemplate is a versioned template; platform starter templates have no tenant.
type DocumentTemplate struct{ ent.Schema }

func (DocumentTemplate) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.UUID("tenant_id", uuid.UUID{}).Optional().Nillable(),
		field.String("code").NotEmpty(),
		field.String("name").NotEmpty(),
		field.String("category").NotEmpty(),
		field.String("tenant_type").Optional(),
		field.Int("version").Default(1),
		field.Text("body").Comment("HTML with {{merge_fields}}"),
		field.Strings("merge_fields").Optional(),
		field.JSON("conditions", map[string]any{}).Optional(),
		field.Enum("status").Values("draft", "approved", "retired").Default("draft"),
		field.UUID("approved_by", uuid.UUID{}).Optional().Nillable(),
		field.Time("approved_at").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (DocumentTemplate) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "code", "version").Unique()}
}

// Document is a generated or uploaded document tracked to execution.
type Document struct{ ent.Schema }

func (Document) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (Document) Fields() []ent.Field {
	return []ent.Field{
		field.String("number").NotEmpty(),
		field.UUID("template_id", uuid.UUID{}).Optional().Nillable(),
		field.Int("template_version").Optional().Nillable(),
		field.String("kind").NotEmpty(),
		field.String("title").NotEmpty(),
		field.String("entity_type").NotEmpty(),
		field.UUID("entity_id", uuid.UUID{}),
		field.UUID("unit_id", uuid.UUID{}).Optional().Nillable(),
		field.Strings("party_ids").Optional(),
		field.String("file_key").Optional(),
		field.String("sha256").Optional(),
		field.String("verification_code").NotEmpty(),
		field.Enum("status").Values("draft", "issued", "partly_signed", "executed", "expired", "superseded").Default("draft"),
		field.Time("issued_at").Optional().Nillable(),
		field.Time("executed_at").Optional().Nillable(),
		field.Time("expires_at").Optional().Nillable(),
		field.JSON("key_dates", []map[string]any{}).Optional(),
		field.Bool("legal_hold").Default(false),
		field.Time("retention_until").Optional().Nillable(),
		createdBy(),
	}
}

func (Document) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "number").Unique(),
		index.Fields("verification_code").Unique(),
		index.Fields("tenant_id", "entity_type", "entity_id"),
	}
}

// DocumentSignature records acceptance by OTP, a wet-signed upload or an e-signature.
type DocumentSignature struct{ ent.Schema }

func (DocumentSignature) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (DocumentSignature) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("document_id", uuid.UUID{}),
		field.UUID("party_id", uuid.UUID{}).Optional().Nillable(),
		field.Enum("method").Values("otp_acceptance", "wet_upload", "e_signature"),
		field.Time("signed_at"),
		field.String("document_hash").Optional(),
		field.JSON("device", map[string]any{}).Optional(),
		field.String("ip").Optional(),
		field.String("scan_file_key").Optional(),
	}
}

func (DocumentSignature) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "document_id")}
}

// DocumentAccessLog records every view and download of a contract or identity document.
type DocumentAccessLog struct{ ent.Schema }

func (DocumentAccessLog) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (DocumentAccessLog) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("document_id", uuid.UUID{}),
		field.UUID("actor_id", uuid.UUID{}).Optional().Nillable(),
		field.String("actor_kind").Optional(),
		field.Enum("action").Values("view", "download"),
		field.String("ip").Optional(),
	}
}

func (DocumentAccessLog) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "document_id", "created_at")}
}

// PrivacyRequest is a data subject request with its statutory deadline.
type PrivacyRequest struct{ ent.Schema }

func (PrivacyRequest) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (PrivacyRequest) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("party_id", uuid.UUID{}).Optional().Nillable(),
		field.String("requester_name").Optional(),
		field.String("requester_phone").Optional(),
		field.Enum("kind").Values("access", "correction", "deletion"),
		field.Enum("status").Values("received", "verified", "in_progress", "completed", "rejected").Default("received"),
		field.Time("received_at"),
		field.Time("due_at"),
		field.Time("completed_at").Optional().Nillable(),
		field.String("export_file_key").Optional(),
		field.Text("notes").Optional(),
	}
}

func (PrivacyRequest) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "status", "due_at")}
}
