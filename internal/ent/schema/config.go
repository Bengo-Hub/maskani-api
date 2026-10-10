package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// TenantSetting holds one tenant's operating configuration. One row per tenant.
type TenantSetting struct{ ent.Schema }

func (TenantSetting) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (TenantSetting) Fields() []ent.Field {
	return []ent.Field{
		field.Enum("tenant_type").
			Values("estate_operator", "property_manager", "landlord", "agent", "owners_association").
			Default("estate_operator"),
		field.String("use_case_preset").Default("estate_developer"),
		field.String("currency").Default("KES"),
		field.String("timezone").Default("Africa/Nairobi"),
		field.Int("billing_day").Default(1).Comment("Day of month invoices are issued"),
		field.Int("due_day").Default(10).Comment("Day of month invoices fall due"),
		field.Int("reading_window_start").Default(25),
		field.Int("reading_window_end").Default(28),
		field.String("quiet_hours_start").Default("21:00"),
		field.String("quiet_hours_end").Default("07:00"),
		field.Enum("allocation_order").Values("oldest_first", "priority").Default("oldest_first"),
		field.Int("bill_to_revert_days").Default(30).Comment("Days after an assigned occupant leaves before unpaid charges revert to the owner"),
		field.Float("water_loss_alert_pct").Default(15),
		field.JSON("arrears_steps", []map[string]any{}).Optional().Comment("Escalation steps: day offset, action, channel"),
		field.String("terms_version").Optional(),
		field.String("privacy_version").Optional(),
		field.String("portal_support_phone").Optional(),
		field.String("portal_support_email").Optional(),
		createdBy(),
	}
}

func (TenantSetting) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id").Unique()}
}

// TenantModule is one module switch for a tenant. Turning a module off keeps its data readable.
type TenantModule struct{ ent.Schema }

func (TenantModule) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (TenantModule) Fields() []ent.Field {
	return []ent.Field{
		field.String("module").NotEmpty(),
		field.Bool("enabled").Default(true),
		field.Time("enabled_at").Optional().Nillable(),
		field.Time("disabled_at").Optional().Nillable(),
		field.UUID("changed_by", uuid.UUID{}).Optional().Nillable(),
	}
}

func (TenantModule) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "module").Unique()}
}

// CatalogEntry is one entry of a configurable catalogue. Platform defaults have no tenant; a
// tenant row with the same kind and code overrides the default for that tenant. The tenant guard
// does not apply here because defaults must be readable by every tenant; the repository always
// filters by (tenant_id = ctx tenant OR tenant_id IS NULL).
type CatalogEntry struct{ ent.Schema }

func (CatalogEntry) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.UUID("tenant_id", uuid.UUID{}).Optional().Nillable(),
		field.String("kind").NotEmpty(),
		field.String("code").NotEmpty(),
		field.String("name").NotEmpty(),
		field.Text("description").Optional(),
		field.String("parent_code").Optional(),
		field.Int("sort").Default(0),
		field.Bool("active").Default(true),
		field.JSON("attrs", map[string]any{}).Optional().Comment("Kind-specific settings, e.g. SLA hours for a work order category or validity for a pass type"),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (CatalogEntry) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("kind", "code").Unique().Annotations(entsql.IndexWhere("tenant_id IS NULL")),
		index.Fields("tenant_id", "kind", "code").Unique().Annotations(entsql.IndexWhere("tenant_id IS NOT NULL")),
	}
}

// CustomFieldDef defines an extra field on a key record type.
type CustomFieldDef struct{ ent.Schema }

func (CustomFieldDef) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (CustomFieldDef) Fields() []ent.Field {
	return []ent.Field{
		field.Enum("entity").Values("property", "unit", "party", "sale_contract", "work_order", "vendor", "lease"),
		field.String("key").NotEmpty(),
		field.String("label").NotEmpty(),
		field.Enum("field_type").Values("text", "number", "date", "boolean", "select", "multi_select"),
		field.Strings("options").Optional(),
		field.Bool("required").Default(false),
		field.Bool("show_in_list").Default(false),
		field.Int("sort").Default(0),
		field.Bool("active").Default(true),
	}
}

func (CustomFieldDef) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "entity", "key").Unique()}
}

// ReminderSchedule sets when and how reminders go out for one reminder kind.
type ReminderSchedule struct{ ent.Schema }

func (ReminderSchedule) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (ReminderSchedule) Fields() []ent.Field {
	return []ent.Field{
		field.Enum("kind").Values("bill", "instalment", "document_expiry", "vendor_licence", "lease_end", "rent_review"),
		field.Ints("offsets_days").Comment("Negative = before the date, positive = after"),
		field.Strings("channels"),
		field.String("template_code").Optional(),
		field.Bool("respect_quiet_hours").Default(true),
		field.Bool("active").Default(true),
	}
}

func (ReminderSchedule) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "kind").Unique()}
}
