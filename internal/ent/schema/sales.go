package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// PriceList prices units by type and phase for a period.
type PriceList struct{ ent.Schema }

func (PriceList) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (PriceList) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("property_id", uuid.UUID{}),
		field.String("name").NotEmpty(),
		field.String("phase").Optional(),
		field.String("currency").Default("KES"),
		field.Time("effective_from"),
		field.Time("effective_to").Optional().Nillable(),
		field.Enum("status").Values("draft", "active", "retired").Default("draft"),
		createdBy(),
	}
}

func (PriceList) Edges() []ent.Edge {
	return []ent.Edge{edge.To("items", PriceListItem.Type)}
}

func (PriceList) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "property_id", "status")}
}

// PriceListItem is a price for a unit type (or one unit) with deposit and term rules.
type PriceListItem struct{ ent.Schema }

func (PriceListItem) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (PriceListItem) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("price_list_id", uuid.UUID{}),
		field.String("unit_type").Optional(),
		field.UUID("unit_id", uuid.UUID{}).Optional().Nillable(),
		money("price"),
		money("reservation_fee"),
		field.Float("deposit_pct").Default(20),
		optMoney("min_deposit"),
		field.Int("max_term_months").Default(24),
		field.Float("interest_rate").Default(0),
		field.JSON("discount_rules", []map[string]any{}).Optional(),
	}
}

func (PriceListItem) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("price_list", PriceList.Type).Ref("items").Field("price_list_id").Unique().Required(),
	}
}

func (PriceListItem) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "price_list_id")}
}

// Reservation holds a unit for a buyer for a set period on payment of a fee.
type Reservation struct{ ent.Schema }

func (Reservation) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (Reservation) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("unit_id", uuid.UUID{}),
		field.UUID("party_id", uuid.UUID{}),
		field.UUID("price_list_item_id", uuid.UUID{}).Optional().Nillable(),
		money("price"),
		money("fee_amount"),
		field.UUID("fee_treasury_invoice_id", uuid.UUID{}).Optional().Nillable(),
		field.Time("fee_paid_at").Optional().Nillable(),
		field.Time("reserved_at"),
		field.Time("expires_at"),
		field.Enum("status").Values("pending_payment", "active", "converted", "expired", "cancelled").Default("pending_payment"),
		field.UUID("sale_contract_id", uuid.UUID{}).Optional().Nillable(),
		createdBy(),
	}
}

func (Reservation) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "status", "expires_at"),
		index.Fields("tenant_id", "unit_id").Unique().
			Annotations(entsql.IndexWhere("status IN ('pending_payment','active')")),
		index.Fields("tenant_id", "created_at", "id"),
	}
}

// SaleContract tracks a unit sale from agreement to title.
type SaleContract struct{ ent.Schema }

func (SaleContract) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (SaleContract) Fields() []ent.Field {
	return []ent.Field{
		field.String("contract_number").NotEmpty(),
		field.UUID("property_id", uuid.UUID{}),
		field.UUID("unit_id", uuid.UUID{}),
		field.UUID("primary_buyer_id", uuid.UUID{}),
		field.JSON("buyers", []map[string]any{}).Optional().Comment(`[{"party_id":"...","share":50}]`),
		field.UUID("reservation_id", uuid.UUID{}).Optional().Nillable(),
		money("price"),
		money("discount"),
		field.String("discount_reason").Optional(),
		money("net_price"),
		money("reservation_credit"),
		money("deposit_amount"),
		field.Enum("payment_option").Values("outright", "instalments", "milestone", "financed").Default("instalments"),
		field.JSON("financier", map[string]any{}).Optional().Comment("mortgage or SACCO: name, offer letter ref, amount"),
		field.Int("term_months").Default(0),
		field.Enum("frequency").Values("monthly", "quarterly", "milestone", "once").Default("monthly"),
		field.Float("interest_rate").Default(0),
		field.JSON("late_charge", map[string]any{}).Optional().Comment("only as the agreement provides; never compounded"),
		field.Int("grace_days").Default(30),
		field.JSON("buyer_advocate", map[string]any{}).Optional(),
		field.JSON("seller_advocate", map[string]any{}).Optional(),
		field.Time("signed_at").Optional().Nillable(),
		field.UUID("agreement_document_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("unit_account_id", uuid.UUID{}).Optional().Nillable().Comment("sales fund account (S-B07)"),
		field.Enum("status").
			Values("draft", "active", "fully_paid", "handed_over", "titled", "in_default", "terminated", "cancelled").
			Default("draft"),
		field.Time("default_since").Optional().Nillable(),
		field.Time("terminated_at").Optional().Nillable(),
		field.JSON("termination", map[string]any{}).Optional(),
		money("invoiced_total"),
		money("paid_total"),
		field.Text("notes").Optional(),
		customFields(),
		createdBy(),
	}
}

func (SaleContract) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("schedules", InstalmentSchedule.Type),
		edge.To("title_stages", TitleStage.Type),
	}
}

func (SaleContract) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "contract_number").Unique(),
		index.Fields("tenant_id", "status"),
		index.Fields("tenant_id", "unit_id"),
		index.Fields("tenant_id", "primary_buyer_id"),
		index.Fields("tenant_id", "property_id", "created_at", "id"),
		index.Fields("tenant_id", "created_at", "id"),
	}
}

// InstalmentSchedule is one version of a contract's payment schedule; a restructure adds a version.
type InstalmentSchedule struct{ ent.Schema }

func (InstalmentSchedule) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (InstalmentSchedule) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("contract_id", uuid.UUID{}),
		field.Int("version").Default(1),
		field.Enum("status").Values("proposed", "active", "superseded", "rejected").Default("active"),
		field.Text("reason").Optional(),
		field.UUID("approved_by", uuid.UUID{}).Optional().Nillable(),
		field.Time("approved_at").Optional().Nillable(),
		field.Time("buyer_accepted_at").Optional().Nillable(),
	}
}

func (InstalmentSchedule) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("contract", SaleContract.Type).Ref("schedules").Field("contract_id").Unique().Required(),
		edge.To("instalments", Instalment.Type),
	}
}

func (InstalmentSchedule) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "contract_id", "version").Unique()}
}

// Instalment is one scheduled payment; it is invoiced in treasury when it falls due.
type Instalment struct{ ent.Schema }

func (Instalment) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (Instalment) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("schedule_id", uuid.UUID{}),
		field.UUID("contract_id", uuid.UUID{}),
		field.Int("seq"),
		field.Enum("kind").Values("reservation", "deposit", "instalment", "milestone", "balance", "financier"),
		field.Time("due_date"),
		money("amount"),
		field.String("milestone_label").Optional(),
		field.Time("released_at").Optional().Nillable(),
		field.UUID("released_by", uuid.UUID{}).Optional().Nillable(),
		field.String("evidence_key").Optional(),
		field.Enum("status").Values("scheduled", "invoiced", "partially_paid", "paid", "overdue", "waived").Default("scheduled"),
		field.UUID("treasury_invoice_id", uuid.UUID{}).Optional().Nillable(),
		field.String("invoice_number").Optional(),
		money("paid_amount"),
		field.Time("paid_at").Optional().Nillable(),
	}
}

func (Instalment) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("schedule", InstalmentSchedule.Type).Ref("instalments").Field("schedule_id").Unique().Required(),
	}
}

func (Instalment) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("schedule_id", "seq").Unique(),
		index.Fields("tenant_id", "status", "due_date"),
		index.Fields("tenant_id", "contract_id"),
	}
}

// Handover records the handover of a fully paid unit: keys, readings, checklist and snags.
type Handover struct{ ent.Schema }

func (Handover) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (Handover) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("contract_id", uuid.UUID{}),
		field.UUID("unit_id", uuid.UUID{}),
		field.Time("scheduled_at").Optional().Nillable(),
		field.Time("completed_at").Optional().Nillable(),
		field.JSON("keys", []map[string]any{}).Optional(),
		field.JSON("readings", []map[string]any{}).Optional(),
		field.JSON("checklist", []map[string]any{}).Optional(),
		field.JSON("snag_items", []map[string]any{}).Optional(),
		field.UUID("signed_document_id", uuid.UUID{}).Optional().Nillable(),
		field.Enum("status").Values("scheduled", "in_progress", "completed", "cancelled").Default("scheduled"),
	}
}

func (Handover) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "contract_id").Unique(), index.Fields("tenant_id", "status")}
}

// TitleStage logs one step towards title release.
type TitleStage struct{ ent.Schema }

func (TitleStage) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (TitleStage) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("contract_id", uuid.UUID{}),
		field.UUID("unit_id", uuid.UUID{}),
		field.String("stage").NotEmpty().Comment("catalog title_stage"),
		field.Enum("status").Values("pending", "in_progress", "done", "blocked").Default("pending"),
		field.Time("stage_date").Optional().Nillable(),
		field.String("reference").Optional(),
		field.UUID("document_id", uuid.UUID{}).Optional().Nillable(),
		field.Text("notes").Optional(),
	}
}

func (TitleStage) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("contract", SaleContract.Type).Ref("title_stages").Field("contract_id").Unique().Required(),
	}
}

func (TitleStage) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "contract_id", "stage").Unique()}
}
