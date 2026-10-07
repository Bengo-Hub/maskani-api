package schema

import (
	"encoding/json"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// Tenant is the local projection of an auth-api tenant. auth-api owns identity and branding; this
// keeps only what routing and joins need.
type Tenant struct{ ent.Schema }

func (Tenant) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.String("name").NotEmpty(),
		field.String("slug").NotEmpty().Unique(),
		field.String("status").Default("active"),
		field.String("use_case").Optional().Nillable(),
		field.String("sync_status").Default("synced"),
		field.Time("last_sync_at").Optional().Nillable(),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (Tenant) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("users", MaskaniUser.Type),
		edge.To("outlets", Outlet.Type),
	}
}

func (Tenant) Indexes() []ent.Index {
	return []ent.Index{index.Fields("status")}
}

// Outlet is the local projection of an auth-api branch. Every Maskani property is one outlet, so
// branch-scoped roles and outlet assignment apply to properties without a second concept.
type Outlet struct{ ent.Schema }

func (Outlet) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.UUID("tenant_id", uuid.UUID{}),
		field.String("tenant_slug").NotEmpty(),
		field.String("code").NotEmpty(),
		field.String("name").NotEmpty(),
		field.JSON("address_json", map[string]any{}).Optional(),
		field.String("status").Default("active"),
		field.String("use_case").Optional().Nillable(),
		field.Bool("is_hq").Default(false).Comment("HQ outlets see every property"),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (Outlet) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("tenant", Tenant.Type).Ref("outlets").Field("tenant_id").Unique().Required(),
	}
}

func (Outlet) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "code").Unique(),
		index.Fields("tenant_slug"),
	}
}

// MaskaniUser is the JIT-provisioned local projection of an auth-api user within one tenant.
// Staff, owners, occupants, vendor supervisors and guards all appear here with their kind.
type MaskaniUser struct{ ent.Schema }

func (MaskaniUser) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.UUID("tenant_id", uuid.UUID{}),
		field.UUID("auth_service_user_id", uuid.UUID{}).Comment("auth-api user id, unique per tenant"),
		field.String("email").Optional(),
		field.String("phone").Optional(),
		field.String("name").Optional(),
		field.Enum("kind").Values("staff", "customer", "vendor_supervisor", "guard").Default("staff"),
		field.String("status").Default("active"),
		field.String("sync_status").Default("synced"),
		field.Time("last_sync_at").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (MaskaniUser) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("tenant", Tenant.Type).Ref("users").Field("tenant_id").Unique().Required(),
		edge.From("role_assignments", UserRoleAssignment.Type).Ref("user"),
	}
}

func (MaskaniUser) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "auth_service_user_id").Unique(),
		index.Fields("tenant_id", "kind", "status"),
	}
}

// MaskaniUserOutlet assigns a staff user to a property (outlet) with a property role. A non-admin
// staff user sees only properties they are assigned to.
type MaskaniUserOutlet struct{ ent.Schema }

func (MaskaniUserOutlet) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.UUID("tenant_id", uuid.UUID{}),
		field.UUID("user_id", uuid.UUID{}).Comment("Local MaskaniUser.ID"),
		field.UUID("outlet_id", uuid.UUID{}).Comment("auth-api outlet id of the property"),
		field.Bool("is_home_outlet").Default(false),
		field.Enum("property_role").
			Values("property_manager", "caretaker", "finance", "sales", "letting", "security", "other").
			Default("other"),
		field.String("erp_employee_id").Optional().Comment("erp-api employee id when the person is on ERP payroll"),
		field.Time("ends_at").Optional().Nillable(),
		field.UUID("assigned_by", uuid.UUID{}).Optional().Nillable(),
		field.Time("assigned_at").Default(time.Now).Immutable(),
	}
}

func (MaskaniUserOutlet) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "user_id", "outlet_id").Unique(),
		index.Fields("tenant_id", "outlet_id"),
	}
}

// MaskaniRole is the role catalogue. Seeded system roles are global (tenant_id null); a tenant may
// clone one (copy on write) or create its own.
type MaskaniRole struct{ ent.Schema }

func (MaskaniRole) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.UUID("tenant_id", uuid.UUID{}).Optional().Nillable(),
		field.String("role_code").NotEmpty(),
		field.String("name").NotEmpty(),
		field.Text("description").Optional(),
		field.Bool("is_system_role").Default(false),
		field.Bool("is_customer_role").Default(false).Comment("Portal roles (owner, occupant, vendor supervisor) never count as staff users"),
		field.UUID("cloned_from_role_id", uuid.UUID{}).Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (MaskaniRole) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("permissions", MaskaniPermission.Type).Through("role_permissions", RolePermission.Type),
		edge.From("user_assignments", UserRoleAssignment.Type).Ref("role"),
	}
}

func (MaskaniRole) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id"),
		index.Fields("role_code").Unique().Annotations(entsql.IndexWhere("tenant_id IS NULL")),
		index.Fields("tenant_id", "role_code").Unique().Annotations(entsql.IndexWhere("tenant_id IS NOT NULL")),
	}
}

// MaskaniPermission is the global permission catalogue (never tenant scoped).
type MaskaniPermission struct{ ent.Schema }

func (MaskaniPermission) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.String("permission_code").NotEmpty().Unique(),
		field.String("name").NotEmpty(),
		field.String("module").NotEmpty(),
		field.String("action").NotEmpty(),
		field.String("resource").Optional(),
		field.Text("description").Optional(),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}

func (MaskaniPermission) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("roles", MaskaniRole.Type).Ref("permissions").Through("role_permissions", RolePermission.Type),
	}
}

func (MaskaniPermission) Indexes() []ent.Index {
	return []ent.Index{index.Fields("module", "action")}
}

// RolePermission is the role to permission junction.
type RolePermission struct{ ent.Schema }

func (RolePermission) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("role_id", uuid.UUID{}),
		field.UUID("permission_id", uuid.UUID{}),
	}
}

func (RolePermission) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("role", MaskaniRole.Type).Field("role_id").Required().Unique(),
		edge.To("permission", MaskaniPermission.Type).Field("permission_id").Required().Unique(),
	}
}

func (RolePermission) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("role_id", "permission_id").Unique(),
		index.Fields("permission_id"),
	}
}

// UserRoleAssignment ties a role to a user within a tenant.
type UserRoleAssignment struct{ ent.Schema }

func (UserRoleAssignment) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.UUID("tenant_id", uuid.UUID{}),
		field.UUID("user_id", uuid.UUID{}),
		field.UUID("role_id", uuid.UUID{}),
		field.UUID("assigned_by", uuid.UUID{}),
		field.Time("assigned_at").Default(time.Now).Immutable(),
		field.Time("expires_at").Optional().Nillable(),
	}
}

func (UserRoleAssignment) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("user", MaskaniUser.Type).Field("user_id").Required().Unique(),
		edge.To("role", MaskaniRole.Type).Field("role_id").Required().Unique(),
	}
}

func (UserRoleAssignment) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "user_id", "role_id").Unique(),
		index.Fields("role_id"),
	}
}

// AuditLog records financial, contract, access and configuration changes. No FK edges, so a row
// stays readable after its target is deleted.
type AuditLog struct{ ent.Schema }

func (AuditLog) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.UUID("tenant_id", uuid.UUID{}),
		field.UUID("actor_user_id", uuid.UUID{}).Comment("auth-api user id of the actor; uuid.Nil for system jobs"),
		field.String("actor_email").Optional(),
		field.String("action").NotEmpty(),
		field.String("target_type").NotEmpty(),
		field.UUID("target_id", uuid.UUID{}),
		field.JSON("before", map[string]any{}).Optional(),
		field.JSON("after", map[string]any{}).Optional(),
		field.String("ip").Optional(),
		field.String("request_id").Optional(),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}

func (AuditLog) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "created_at"),
		index.Fields("tenant_id", "target_type", "target_id"),
	}
}

// OutboxEvent matches the shared-events SQL outbox repository column layout exactly.
type OutboxEvent struct{ ent.Schema }

func (OutboxEvent) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.UUID("tenant_id", uuid.UUID{}),
		field.String("aggregate_type").NotEmpty(),
		field.String("aggregate_id").NotEmpty(),
		field.String("event_type").NotEmpty(),
		field.JSON("payload", json.RawMessage{}),
		field.String("status").Default("PENDING"),
		field.Int("attempts").Default(0),
		field.Time("last_attempt_at").Optional().Nillable(),
		field.Time("published_at").Optional().Nillable(),
		field.Text("error_message").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}

func (OutboxEvent) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status", "created_at"),
		index.Fields("tenant_id", "status"),
	}
}

// ConsumedEvent makes event consumers idempotent on (event id, consumer).
type ConsumedEvent struct{ ent.Schema }

func (ConsumedEvent) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.UUID("event_id", uuid.UUID{}),
		field.String("consumer").NotEmpty(),
		field.UUID("tenant_id", uuid.UUID{}).Optional().Nillable(),
		field.String("subject").Optional(),
		field.Time("processed_at").Default(time.Now).Immutable(),
	}
}

func (ConsumedEvent) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("event_id", "consumer").Unique(),
		index.Fields("processed_at"),
	}
}

// DocumentSequence is the per-tenant counter behind human-readable numbers (contracts, work
// orders, incidents, documents). Allocation takes a row lock in a transaction.
type DocumentSequence struct{ ent.Schema }

func (DocumentSequence) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.UUID("tenant_id", uuid.UUID{}),
		field.String("kind").NotEmpty(),
		field.String("prefix").Optional(),
		field.Int64("next_value").Default(1),
		field.Int("pad_width").Default(5),
		field.String("format").Optional().Comment("Template: {prefix} {seq} {yy} {yyyy} {mm}; empty = {prefix}{seq}"),
		field.String("reset_period").Default("none"),
		field.String("period_key").Optional(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (DocumentSequence) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "kind").Unique()}
}
