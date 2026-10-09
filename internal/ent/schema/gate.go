package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// GateDevice is a tablet registered to a property's gate.
type GateDevice struct{ ent.Schema }

func (GateDevice) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (GateDevice) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("property_id", uuid.UUID{}),
		field.String("name").NotEmpty(),
		field.String("gate_name").Default("Main gate"),
		field.String("device_key_hash").NotEmpty().Sensitive(),
		field.UUID("registered_by", uuid.UUID{}).Optional().Nillable(),
		field.Time("last_seen_at").Optional().Nillable(),
		field.String("app_version").Optional(),
		field.Bool("offline_alerted").Default(false),
		field.Enum("status").Values("active", "revoked").Default("active"),
	}
}

func (GateDevice) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "property_id", "status"), index.Fields("device_key_hash").Unique(),
		// The offline-tablet job scans every tenant for active devices not yet alerted.
		index.Fields("last_seen_at").Annotations(entsql.IndexWhere("status = 'active' AND offline_alerted = false"))}
}

// GuardPost is a manned post with guard counts per shift.
type GuardPost struct{ ent.Schema }

func (GuardPost) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (GuardPost) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("property_id", uuid.UUID{}),
		field.UUID("contract_id", uuid.UUID{}).Optional().Nillable(),
		field.String("name").NotEmpty(),
		field.Int("guards_day").Default(1),
		field.Int("guards_night").Default(1),
		field.JSON("shift_pattern", map[string]any{}).Optional(),
		field.Bool("active").Default(true),
	}
}

func (GuardPost) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "property_id")}
}

// Roster is one guard's shift at a post, compared with sign-ons for coverage.
type Roster struct{ ent.Schema }

func (Roster) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (Roster) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("post_id", uuid.UUID{}),
		field.UUID("personnel_id", uuid.UUID{}),
		field.Time("shift_date"),
		field.Enum("shift").Values("day", "night"),
		field.Time("starts_at"),
		field.Time("ends_at"),
		field.Time("signed_on_at").Optional().Nillable(),
		field.Time("signed_off_at").Optional().Nillable(),
		field.Enum("status").Values("scheduled", "on_duty", "completed", "absent").Default("scheduled"),
	}
}

func (Roster) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "post_id", "personnel_id", "shift_date", "shift").Unique()}
}

// PatrolCheckpoint is a QR tag on a patrol route.
type PatrolCheckpoint struct{ ent.Schema }

func (PatrolCheckpoint) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (PatrolCheckpoint) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("property_id", uuid.UUID{}),
		field.String("name").NotEmpty(),
		field.String("code").NotEmpty(),
		field.String("location_note").Optional(),
		field.Int("sort").Default(0),
		field.Bool("active").Default(true),
	}
}

func (PatrolCheckpoint) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "code").Unique()}
}

// PatrolScan is one checkpoint scan. Grows fast: BRIN on scanned_at is added by a hand-written migration.
type PatrolScan struct{ ent.Schema }

func (PatrolScan) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (PatrolScan) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("property_id", uuid.UUID{}),
		field.UUID("checkpoint_id", uuid.UUID{}),
		field.UUID("personnel_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("device_id", uuid.UUID{}).Optional().Nillable(),
		field.Time("scanned_at"),
		field.String("round_ref").Optional(),
		field.String("client_event_id").NotEmpty(),
	}
}

func (PatrolScan) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "device_id", "client_event_id").Unique(),
		index.Fields("tenant_id", "property_id", "scanned_at"),
	}
}

// VisitorPass lets a visitor in by QR or 6-digit code. Codes are stored only as hashes.
type VisitorPass struct{ ent.Schema }

func (VisitorPass) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (VisitorPass) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("property_id", uuid.UUID{}),
		field.UUID("unit_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("host_party_id", uuid.UUID{}).Optional().Nillable(),
		field.Enum("created_by_kind").Values("resident", "staff", "guard").Default("resident"),
		field.UUID("created_by_id", uuid.UUID{}).Optional().Nillable(),
		field.Enum("pass_type").Values("guest_single", "guest_recurring", "domestic_staff", "delivery", "contractor", "agency").Default("guest_single"),
		field.String("visitor_name").NotEmpty(),
		field.String("visitor_phone").Optional(),
		field.String("vehicle_plate").Optional(),
		field.String("code_hash").NotEmpty().Sensitive(),
		field.String("code_hint").Optional().Comment("last two digits, for staff lookups"),
		field.String("qr_token_hash").Optional().Sensitive(),
		field.Time("valid_from"),
		field.Time("valid_to"),
		field.JSON("recurrence", map[string]any{}).Optional().Comment(`{"days":[1,2,3],"from":"08:00","to":"17:00"}`),
		field.Int("max_entries").Default(1),
		field.Int("entries_used").Default(0),
		field.UUID("work_order_id", uuid.UUID{}).Optional().Nillable(),
		field.Enum("status").Values("active", "used", "expired", "cancelled").Default("active"),
		field.Text("notes").Optional(),
	}
}

func (VisitorPass) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "property_id", "status", "valid_to"),
		index.Fields("tenant_id", "code_hash"),
		index.Fields("tenant_id", "qr_token_hash"),
		index.Fields("tenant_id", "host_party_id"),
		index.Fields("tenant_id", "property_id", "created_at", "id"),
		index.Fields("tenant_id", "created_at", "id"),
	}
}

// GateEvent is an entry, exit, denial or walk-in record. Idempotent on (device, client_event_id)
// so offline uploads can be retried. Retention 90 days.
type GateEvent struct{ ent.Schema }

func (GateEvent) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (GateEvent) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("property_id", uuid.UUID{}),
		field.UUID("device_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("pass_id", uuid.UUID{}).Optional().Nillable(),
		field.Enum("kind").Values("entry", "exit", "denied", "walk_in_request", "walk_in_approved", "walk_in_declined"),
		field.String("visitor_name").Optional(),
		field.String("visitor_phone").Optional(),
		field.UUID("host_unit_id", uuid.UUID{}).Optional().Nillable(),
		field.String("vehicle_plate").Optional(),
		field.Bool("id_sighted").Default(false),
		field.Time("occurred_at"),
		field.Bool("offline").Default(false),
		field.String("client_event_id").NotEmpty(),
		field.UUID("guard_personnel_id", uuid.UUID{}).Optional().Nillable(),
		field.Enum("decision").Values("pending", "approved", "declined", "timeout", "none").Default("none"),
		field.Time("decided_at").Optional().Nillable(),
		field.Text("notes").Optional(),
	}
}

func (GateEvent) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "device_id", "client_event_id").Unique(),
		index.Fields("tenant_id", "property_id", "occurred_at"),
		// The gate log pages by (created_at DESC, id DESC) per property.
		index.Fields("tenant_id", "property_id", "created_at", "id"),
		// The 90-day retention purge scans every tenant by age.
		index.Fields("occurred_at"),
	}
}

// Incident is a security or safety incident.
type Incident struct{ ent.Schema }

func (Incident) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (Incident) Fields() []ent.Field {
	return []ent.Field{
		field.String("number").NotEmpty(),
		field.UUID("property_id", uuid.UUID{}),
		field.UUID("unit_id", uuid.UUID{}).Optional().Nillable(),
		field.String("category").NotEmpty().Comment("intrusion, theft, fire, medical, dispute, damage, other"),
		field.Enum("severity").Values("low", "medium", "high", "critical").Default("medium"),
		field.String("title").NotEmpty(),
		field.Text("description").Optional(),
		field.Time("occurred_at"),
		field.String("reported_by_kind").Optional(),
		field.UUID("reported_by_id", uuid.UUID{}).Optional().Nillable(),
		field.Strings("photos").Optional(),
		field.Enum("status").Values("open", "investigating", "resolved", "closed").Default("open"),
		field.Time("notified_at").Optional().Nillable(),
		field.Text("resolution").Optional(),
	}
}

func (Incident) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "number").Unique(), index.Fields("tenant_id", "property_id", "status"),
		index.Fields("tenant_id", "property_id", "created_at", "id"), index.Fields("tenant_id", "created_at", "id")}
}

// OccurrenceEntry is a digital occurrence book line (shift handover, notes).
type OccurrenceEntry struct{ ent.Schema }

func (OccurrenceEntry) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (OccurrenceEntry) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("property_id", uuid.UUID{}),
		field.UUID("post_id", uuid.UUID{}).Optional().Nillable(),
		field.Enum("kind").Values("handover", "note").Default("note"),
		field.Text("body"),
		field.UUID("author_id", uuid.UUID{}).Optional().Nillable(),
		field.String("author_kind").Optional(),
	}
}

func (OccurrenceEntry) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "property_id", "created_at")}
}
