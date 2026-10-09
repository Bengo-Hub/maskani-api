package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// Vendor is a service provider; money side (bills, payouts) lives on the treasury vendor.
type Vendor struct{ ent.Schema }

func (Vendor) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (Vendor) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("treasury_vendor_id", uuid.UUID{}).Optional().Nillable(),
		field.String("name").NotEmpty(),
		field.Strings("categories").Optional().Comment("catalog vendor_category codes"),
		field.String("kra_pin_enc").Optional().Sensitive(),
		field.String("registration_number").Optional(),
		field.String("contact_name").Optional(),
		field.String("phone").Optional(),
		field.String("email").Optional(),
		field.JSON("payment_details", map[string]any{}).Optional().Comment("locked once verified; changes need director approval"),
		field.Time("payment_details_verified_at").Optional().Nillable(),
		field.Float("rating").Optional().Nillable(),
		field.Enum("status").Values("active", "suspended", "inactive").Default("active"),
		customFields(),
		createdBy(),
	}
}

func (Vendor) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("documents", VendorDocument.Type),
		edge.To("contracts", VendorContract.Type),
		edge.To("personnel", VendorPersonnel.Type),
	}
}

func (Vendor) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "status"), index.Fields("tenant_id", "name"),
		index.Fields("tenant_id", "created_at", "id")}
}

// VendorDocument is a licence or compliance document with an expiry date.
type VendorDocument struct{ ent.Schema }

func (VendorDocument) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (VendorDocument) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("vendor_id", uuid.UUID{}),
		field.String("doc_type").NotEmpty().Comment("catalog vendor_doc_type: psra, nema, pcpb, epra, nca, business_permit, insurance, kra"),
		field.String("number").Optional(),
		field.Time("issued_at").Optional().Nillable(),
		field.Time("expires_at").Optional().Nillable(),
		field.String("file_key").Optional(),
		field.Enum("status").Values("valid", "expiring", "expired").Default("valid"),
		field.UUID("verified_by", uuid.UUID{}).Optional().Nillable(),
		field.Int("last_alert_days").Optional().Nillable().Comment("last expiry alert sent at this many days before"),
	}
}

func (VendorDocument) Edges() []ent.Edge {
	return []ent.Edge{edge.From("vendor", Vendor.Type).Ref("documents").Field("vendor_id").Unique().Required()}
}

func (VendorDocument) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "expires_at"), index.Fields("tenant_id", "vendor_id")}
}

// VendorContract is a service contract with SLAs and service credits.
type VendorContract struct{ ent.Schema }

func (VendorContract) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (VendorContract) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("vendor_id", uuid.UUID{}),
		field.UUID("property_id", uuid.UUID{}),
		field.String("contract_number").Optional(),
		field.String("service_category").NotEmpty(),
		field.Text("scope").Optional(),
		field.Enum("fee_basis").Values("fixed_monthly", "per_visit", "per_post", "rate_card").Default("fixed_monthly"),
		money("fee_amount"),
		field.JSON("sla", map[string]any{}).Optional(),
		field.JSON("service_credits", []map[string]any{}).Optional(),
		field.Time("starts_on"),
		field.Time("ends_on").Optional().Nillable(),
		field.Int("renewal_notice_days").Default(30),
		field.Bool("auto_renew").Default(false),
		field.String("budget_line_code").Optional(),
		field.UUID("document_id", uuid.UUID{}).Optional().Nillable(),
		field.Enum("status").Values("draft", "active", "expired", "terminated").Default("active"),
	}
}

func (VendorContract) Edges() []ent.Edge {
	return []ent.Edge{edge.From("vendor", Vendor.Type).Ref("contracts").Field("vendor_id").Unique().Required()}
}

func (VendorContract) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "property_id", "status"), index.Fields("tenant_id", "ends_on")}
}

// ServiceSchedule is a recurring visit plan under a contract (garbage days, cleaning rosters).
type ServiceSchedule struct{ ent.Schema }

func (ServiceSchedule) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (ServiceSchedule) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("contract_id", uuid.UUID{}),
		field.UUID("property_id", uuid.UUID{}),
		field.String("name").NotEmpty(),
		field.Enum("frequency").Values("daily", "weekly", "monthly", "quarterly", "half_yearly"),
		field.Ints("days").Optional().Comment("weekday numbers 0 to 6 or month days"),
		field.String("time_window").Optional(),
		field.String("zone").Optional(),
		field.JSON("checklist", []map[string]any{}).Optional(),
		field.Bool("active").Default(true),
	}
}

func (ServiceSchedule) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "contract_id")}
}

// ServiceVisit is one expected visit with its evidence.
type ServiceVisit struct{ ent.Schema }

func (ServiceVisit) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (ServiceVisit) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("schedule_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("contract_id", uuid.UUID{}),
		field.UUID("vendor_id", uuid.UUID{}),
		field.UUID("property_id", uuid.UUID{}),
		field.Time("due_at"),
		field.Time("checked_in_at").Optional().Nillable(),
		field.Time("checked_out_at").Optional().Nillable(),
		field.Enum("status").Values("scheduled", "completed", "partial", "missed").Default("scheduled"),
		field.Strings("photos").Optional(),
		field.JSON("checklist_result", []map[string]any{}).Optional(),
		field.Int("resident_rating").Optional().Nillable(),
	}
}

func (ServiceVisit) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "property_id", "due_at"), index.Fields("tenant_id", "contract_id", "status")}
}

// VendorPersonnel is an agency person deployed to the estate (guards, cleaners) with a badge.
type VendorPersonnel struct{ ent.Schema }

func (VendorPersonnel) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (VendorPersonnel) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("vendor_id", uuid.UUID{}),
		field.String("full_name").NotEmpty(),
		field.String("role").Optional().Comment("guard, supervisor, cleaner, gardener"),
		field.String("phone").Optional(),
		field.String("badge_number").NotEmpty(),
		field.String("pin_hash").Optional().Sensitive().Comment("gate PIN for guards"),
		field.String("photo_key").Optional(),
		field.Strings("property_ids").Optional(),
		field.Enum("status").Values("active", "deactivated").Default("active"),
		field.Time("deactivated_at").Optional().Nillable(),
	}
}

func (VendorPersonnel) Edges() []ent.Edge {
	return []ent.Edge{edge.From("vendor", Vendor.Type).Ref("personnel").Field("vendor_id").Unique().Required()}
}

func (VendorPersonnel) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "badge_number").Unique(), index.Fields("tenant_id", "vendor_id", "status")}
}

// MaintenanceSchedule generates preventive work orders for shared assets.
type MaintenanceSchedule struct{ ent.Schema }

func (MaintenanceSchedule) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (MaintenanceSchedule) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("property_id", uuid.UUID{}),
		field.String("asset_ref").Optional().Comment("erp-api asset id"),
		field.String("title").NotEmpty(),
		field.String("category").NotEmpty(),
		field.Enum("frequency").Values("weekly", "monthly", "quarterly", "half_yearly", "annual"),
		field.Time("next_due_at"),
		field.UUID("default_vendor_id", uuid.UUID{}).Optional().Nillable(),
		field.String("default_employee_id").Optional(),
		field.Bool("active").Default(true),
	}
}

func (MaintenanceSchedule) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "active", "next_due_at")}
}

// WorkOrder covers a resident request through to closure; requests are work orders in status requested.
type WorkOrder struct{ ent.Schema }

func (WorkOrder) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (WorkOrder) Fields() []ent.Field {
	return []ent.Field{
		field.String("number").NotEmpty(),
		field.UUID("property_id", uuid.UUID{}),
		field.UUID("unit_id", uuid.UUID{}).Optional().Nillable(),
		field.String("area").Optional().Comment("common area label when not in a unit"),
		field.String("category").NotEmpty().Comment("catalog wo_category"),
		field.Enum("priority").Values("emergency", "high", "normal", "low").Default("normal"),
		field.String("title").NotEmpty(),
		field.Text("description").Optional(),
		field.Enum("source").Values("resident", "staff", "schedule", "inspection", "gate").Default("staff"),
		field.UUID("requested_by_party_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("requested_by_user_id", uuid.UUID{}).Optional().Nillable(),
		field.Enum("assignee_kind").Values("vendor", "staff", "none").Default("none"),
		field.UUID("vendor_id", uuid.UUID{}).Optional().Nillable(),
		field.String("erp_employee_id").Optional(),
		field.UUID("assigned_user_id", uuid.UUID{}).Optional().Nillable(),
		field.Time("response_due_at").Optional().Nillable(),
		field.Time("resolution_due_at").Optional().Nillable(),
		field.Time("responded_at").Optional().Nillable(),
		field.Time("completed_at").Optional().Nillable(),
		field.Time("confirmed_at").Optional().Nillable(),
		field.Int("reopened_count").Default(0),
		field.Bool("sla_breached").Default(false),
		optMoney("quote_amount"),
		field.Enum("quote_status").Values("none", "pending", "approved", "rejected").Default("none"),
		money("cost_amount"),
		field.Bool("recharge").Default(false),
		field.UUID("recharge_unit_account_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("recharge_invoice_id", uuid.UUID{}).Optional().Nillable(),
		field.Strings("photos_before").Optional(),
		field.Strings("photos_after").Optional(),
		field.JSON("parts", []map[string]any{}).Optional(),
		field.Int("minutes_on_site").Default(0),
		field.String("asset_ref").Optional(),
		field.UUID("maintenance_schedule_id", uuid.UUID{}).Optional().Nillable(),
		field.Enum("status").
			Values("requested", "triaged", "assigned", "quoted", "approved", "in_progress", "completed", "confirmed", "reopened", "closed", "cancelled").
			Default("requested"),
		customFields(),
		createdBy(),
	}
}

func (WorkOrder) Edges() []ent.Edge {
	return []ent.Edge{edge.To("events", WorkOrderEvent.Type)}
}

func (WorkOrder) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "number").Unique(),
		index.Fields("tenant_id", "property_id", "status", "priority"),
		index.Fields("tenant_id", "resolution_due_at").
			Annotations(entsql.IndexWhere("status NOT IN ('completed','confirmed','closed','cancelled')")),
		index.Fields("tenant_id", "requested_by_party_id"),
		// Keyset lists, by property and tenant wide.
		index.Fields("tenant_id", "property_id", "created_at", "id"),
		index.Fields("tenant_id", "created_at", "id"),
		// The SLA breach job scans every tenant: only open, unflagged orders, by due time.
		index.Fields("resolution_due_at").
			Annotations(entsql.IndexWhere("sla_breached = false AND status NOT IN ('completed','confirmed','closed','cancelled')")),
	}
}

// WorkOrderEvent is the timeline of a work order.
type WorkOrderEvent struct{ ent.Schema }

func (WorkOrderEvent) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (WorkOrderEvent) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("work_order_id", uuid.UUID{}),
		field.String("kind").NotEmpty(),
		field.String("from_status").Optional(),
		field.String("to_status").Optional(),
		field.Text("note").Optional(),
		field.UUID("actor_id", uuid.UUID{}).Optional().Nillable(),
		field.String("actor_kind").Optional(),
	}
}

func (WorkOrderEvent) Edges() []ent.Edge {
	return []ent.Edge{edge.From("work_order", WorkOrder.Type).Ref("events").Field("work_order_id").Unique().Required()}
}

func (WorkOrderEvent) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "work_order_id", "created_at")}
}
