package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// Fund is a separately banked money pool (estate service fund, sales collections, deposits,
// client rent). Each fund has its own paybill, bank account and ledger accounts in treasury, and
// a payment to one fund's account reference can never settle another fund's invoices.
type Fund struct{ ent.Schema }

func (Fund) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (Fund) Fields() []ent.Field {
	return []ent.Field{
		field.String("code").NotEmpty().Comment("estate, sales, deposits, client_rent, sinking"),
		field.String("name").NotEmpty(),
		field.Enum("kind").Values("estate", "sales", "deposits", "client_rent", "reserve", "other").Default("estate"),
		field.UUID("treasury_bank_account_id", uuid.UUID{}).Optional().Nillable(),
		field.String("paybill_shortcode").Optional(),
		field.String("account_prefix").Optional().Comment(`"" for estate (B07), "S-" for sales (S-B07)`),
		field.String("cost_center_code").Optional(),
		field.String("income_account_code").Optional(),
		field.String("receivable_account_code").Optional(),
		field.Bool("is_default").Default(false),
		field.Enum("status").Values("active", "archived").Default("active"),
	}
}

func (Fund) Edges() []ent.Edge {
	return []ent.Edge{edge.To("accounts", UnitAccount.Type)}
}

func (Fund) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "code").Unique()}
}

// ChargeType is one entry of the tenant's charge catalogue, seeded from platform defaults or
// custom. Rates live in ChargeRate so issued invoices never change when a rate does.
type ChargeType struct{ ent.Schema }

func (ChargeType) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (ChargeType) Fields() []ent.Field {
	return []ent.Field{
		field.String("code").NotEmpty(),
		field.String("name").NotEmpty(),
		field.Text("description").Optional(),
		field.Enum("charge_group").Values("occupancy", "services", "utilities", "reserves", "amenities", "recoveries", "sales"),
		field.Enum("basis").Values("fixed", "per_unit_type", "per_sqm", "entitlement", "metered", "percentage", "one_off"),
		field.Enum("frequency").Values("monthly", "quarterly", "half_yearly", "annual", "on_event", "one_off").Default("monthly"),
		field.String("event_trigger").Optional().Comment("move_in, move_out, handover, booking"),
		field.JSON("applies_to", map[string]any{}).Optional().Comment(`{"scope":"tenant|properties|unit_types|units|opt_in","ids":[...]}`),
		field.Enum("bill_to").Values("owner", "occupant", "landlord", "buyer").Default("owner"),
		field.Bool("reassignable").Default(false).Comment("owner may assign it to the occupant"),
		field.String("fund_code").Default("estate"),
		field.String("ledger_account_code").Optional(),
		field.String("cost_center_code").Optional(),
		field.Float("vat_rate").Default(0),
		field.Bool("tax_exempt").Default(true),
		field.String("etims_item_code").Optional(),
		field.Bool("wht_applicable").Default(false),
		field.Enum("proration").Values("none", "days", "full_month").Default("days"),
		field.JSON("penalty", map[string]any{}).Optional().Comment(`{"enabled":false,"grace_days":0,"rate_pct":0,"flat_amount":0,"cap":0}; never compounded`),
		field.Int("allocation_priority").Default(100).Comment("lower settles first when allocation_order is priority; late charges always last"),
		field.String("percentage_of_code").Optional(),
		field.Enum("tariff_kind").Values("flat", "block").Default("flat"),
		field.String("seeded_from").Optional().Comment("platform catalogue code this was enabled from"),
		field.Int("sort").Default(0),
		field.Bool("active").Default(true),
	}
}

func (ChargeType) Edges() []ent.Edge {
	return []ent.Edge{edge.To("rates", ChargeRate.Type)}
}

func (ChargeType) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "code").Unique()}
}

// ChargeRate is a dated rate for a charge at a scope. The most specific scope wins
// (unit over unit type over property over tenant) among rates effective on the billing date.
type ChargeRate struct{ ent.Schema }

func (ChargeRate) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (ChargeRate) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("charge_type_id", uuid.UUID{}),
		field.Enum("scope").Values("tenant", "property", "unit_type", "unit").Default("tenant"),
		field.UUID("property_id", uuid.UUID{}).Optional().Nillable(),
		field.String("unit_type").Optional(),
		field.UUID("unit_id", uuid.UUID{}).Optional().Nillable(),
		money("amount", "fixed amount, per-sqm rate, per-m3 rate for flat tariffs, or percentage"),
		field.JSON("tariff", []map[string]any{}).Optional().Comment(`block tariff: [{"from":0,"to":6,"rate":120}]`),
		money("fixed_meter_charge"),
		field.Time("effective_from"),
		field.Time("effective_to").Optional().Nillable(),
		field.Text("notes").Optional(),
		createdBy(),
	}
}

func (ChargeRate) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("charge_type", ChargeType.Type).Ref("rates").Field("charge_type_id").Unique().Required(),
	}
}

func (ChargeRate) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "charge_type_id", "scope", "effective_from")}
}

// UnitCharge is an opt-in or unit-specific charge (a second parking bay, a storage room).
type UnitCharge struct{ ent.Schema }

func (UnitCharge) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (UnitCharge) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("unit_id", uuid.UUID{}),
		field.UUID("charge_type_id", uuid.UUID{}),
		field.UUID("party_id", uuid.UUID{}).Optional().Nillable(),
		qty("quantity"),
		optMoney("amount_override"),
		field.Time("start_date"),
		field.Time("end_date").Optional().Nillable(),
		field.Enum("status").Values("active", "ended").Default("active"),
	}
}

func (UnitCharge) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "unit_id", "status")}
}

// UnitAccount is a unit's account in one fund. account_ref is the paybill account number and is
// registered in treasury as a C2B account route.
type UnitAccount struct{ ent.Schema }

func (UnitAccount) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (UnitAccount) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("unit_id", uuid.UUID{}),
		field.UUID("fund_id", uuid.UUID{}),
		field.String("account_ref").NotEmpty().Comment("normalised: upper case, no spaces or dashes except the fund prefix dash"),
		field.UUID("primary_party_id", uuid.UUID{}).Optional().Nillable(),
		field.String("customer_name").Optional(),
		field.String("customer_phone").Optional(),
		field.Time("c2b_route_registered_at").Optional().Nillable(),
		money("balance", "display cache from treasury; treasury stays authoritative"),
		money("opening_balance"),
		field.Time("balance_synced_at").Optional().Nillable(),
		field.Time("last_payment_at").Optional().Nillable(),
		field.Enum("status").Values("active", "closed").Default("active"),
	}
}

func (UnitAccount) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("unit", Unit.Type).Ref("accounts").Field("unit_id").Unique().Required(),
		edge.From("fund", Fund.Type).Ref("accounts").Field("fund_id").Unique().Required(),
	}
}

func (UnitAccount) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "account_ref").Unique(),
		index.Fields("tenant_id", "unit_id", "fund_id").Unique(),
		index.Fields("tenant_id", "primary_party_id"),
		// Keyset lists: accounts newest first, arrears largest balance first.
		index.Fields("tenant_id", "created_at", "id"),
		index.Fields("tenant_id", "balance", "id"),
		// The paybill route job scans every tenant for accounts not yet registered with treasury.
		index.Fields("created_at").Annotations(entsql.IndexWhere("c2b_route_registered_at IS NULL AND status = 'active'")),
	}
}

// BillingRun issues one invoice per unit account for a property, fund and period.
type BillingRun struct{ ent.Schema }

func (BillingRun) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (BillingRun) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("property_id", uuid.UUID{}),
		field.UUID("fund_id", uuid.UUID{}),
		field.String("period").NotEmpty().Comment("YYYY-MM"),
		field.Enum("run_kind").Values("regular", "adhoc").Default("regular"),
		field.Enum("status").Values("draft", "issuing", "issued", "partially_failed", "cancelled").Default("draft"),
		field.Time("invoice_date"),
		field.Time("due_date"),
		field.Int("unit_count").Default(0),
		field.Int("line_count").Default(0),
		money("total_amount"),
		field.Int("issued_count").Default(0),
		field.Int("failed_count").Default(0),
		field.Int("skipped_count").Default(0),
		field.UUID("started_by", uuid.UUID{}).Optional().Nillable(),
		field.Time("issued_at").Optional().Nillable(),
		field.Text("error").Optional(),
	}
}

func (BillingRun) Edges() []ent.Edge {
	return []ent.Edge{edge.To("lines", BillingRunLine.Type)}
}

func (BillingRun) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "property_id", "fund_id", "period", "run_kind").Unique().
			Annotations(entsql.IndexWhere("status <> 'cancelled'")),
		index.Fields("tenant_id", "status"),
		index.Fields("tenant_id", "property_id", "created_at", "id"),
		index.Fields("tenant_id", "created_at", "id"),
		// The resume job scans every tenant for runs left issuing.
		index.Fields("updated_at").Annotations(entsql.IndexWhere("status = 'issuing'")),
	}
}

// BillingRunLine is one unit account's invoice within a run.
type BillingRunLine struct{ ent.Schema }

func (BillingRunLine) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (BillingRunLine) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("run_id", uuid.UUID{}),
		field.UUID("unit_id", uuid.UUID{}),
		field.UUID("unit_account_id", uuid.UUID{}),
		field.UUID("party_id", uuid.UUID{}).Optional().Nillable(),
		field.String("unit_code").NotEmpty(),
		field.JSON("lines", []map[string]any{}).Comment("charge_code, description, quantity, rate, amount, tax_rate, tax"),
		money("subtotal"),
		money("tax_total"),
		money("total"),
		field.Enum("status").Values("pending", "issued", "failed", "skipped").Default("pending"),
		field.String("skip_reason").Optional(),
		field.UUID("treasury_invoice_id", uuid.UUID{}).Optional().Nillable(),
		field.String("invoice_number").Optional(),
		field.Int("attempts").Default(0),
		field.Text("last_error").Optional(),
	}
}

func (BillingRunLine) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("run", BillingRun.Type).Ref("lines").Field("run_id").Unique().Required(),
	}
}

func (BillingRunLine) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("run_id", "unit_account_id").Unique(),
		index.Fields("tenant_id", "status"),
		index.Fields("tenant_id", "unit_account_id"),
		index.Fields("tenant_id", "treasury_invoice_id"), // payment consumer: invoice to run line
	}
}

// Adjustment is a credit note, waiver, debit or write-off on an account, under approval rules.
// Metadata keeps what the queue shows (account_ref, unit_code, invoice_number, requested_by_name).
type Adjustment struct{ ent.Schema }

func (Adjustment) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (Adjustment) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("unit_account_id", uuid.UUID{}),
		field.UUID("property_id", uuid.UUID{}),
		field.Enum("kind").Values("credit_note", "debit", "waiver", "write_off"),
		money("amount"),
		field.Text("reason"),
		field.UUID("treasury_invoice_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("treasury_credit_note_id", uuid.UUID{}).Optional().Nillable(),
		field.Enum("status").Values("pending_approval", "approved", "rejected", "applied").Default("pending_approval"),
		field.UUID("requested_by", uuid.UUID{}),
		field.JSON("approvals", []map[string]any{}).Optional(),
	}
}

func (Adjustment) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "status"), index.Fields("tenant_id", "property_id", "status", "created_at"),
		index.Fields("unit_account_id", "created_at")}
}

// BillQuery is a resident's query about a bill, handled in a finance queue. Metadata keeps what
// the queue shows (account_ref, unit_code, invoice_number, raised_by_name, answered_by_name).
type BillQuery struct{ ent.Schema }

func (BillQuery) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (BillQuery) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("unit_account_id", uuid.UUID{}),
		field.UUID("property_id", uuid.UUID{}),
		field.UUID("treasury_invoice_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("party_id", uuid.UUID{}).Optional().Nillable(),
		field.String("subject").NotEmpty(),
		field.Text("body"),
		field.Enum("status").Values("open", "in_review", "resolved", "rejected").Default("open"),
		field.UUID("assigned_to", uuid.UUID{}).Optional().Nillable(),
		field.Time("due_by").Optional().Nillable(),
		field.Text("resolution").Optional(),
	}
}

func (BillQuery) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "status", "due_by"), index.Fields("tenant_id", "property_id", "status", "created_at"),
		index.Fields("unit_account_id", "created_at")}
}

// ManualPayment is money staff record that did not come through a gateway prompt or the paybill
// route: a bank transfer, cash, a cheque, or an M-Pesa payment typed in from a statement. It
// waits for review; a property manager or caretaker who can verify (billing.verify) approves it,
// and only then is it booked in treasury against the account. The submitter cannot approve their
// own entry.
type ManualPayment struct{ ent.Schema }

func (ManualPayment) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (ManualPayment) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("unit_account_id", uuid.UUID{}),
		field.UUID("property_id", uuid.UUID{}),
		money("amount"),
		field.Enum("method").Values("bank_transfer", "cash", "cheque", "mpesa"),
		field.String("reference").NotEmpty().Comment("bank slip, cheque number, receipt or M-Pesa code"),
		field.Time("paid_on").SchemaType(map[string]string{"postgres": "date"}),
		field.String("payer_name").Optional(),
		field.Text("note").Optional(),
		field.String("evidence_key").Optional().Comment("media key of a slip or cheque photo"),
		field.Enum("status").Values("pending", "approved", "rejected").Default("pending"),
		field.UUID("submitted_by", uuid.UUID{}),
		field.String("submitted_by_name").Optional(),
		field.UUID("reviewed_by", uuid.UUID{}).Optional().Nillable(),
		field.String("reviewed_by_name").Optional(),
		field.Time("reviewed_at").Optional().Nillable(),
		field.Text("review_note").Optional(),
		field.UUID("treasury_intent_id", uuid.UUID{}).Optional().Nillable(),
	}
}

func (ManualPayment) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "status", "created_at"),
		index.Fields("tenant_id", "unit_account_id", "created_at"),
		// One slip once: a rejected entry can be corrected and submitted again.
		index.Fields("tenant_id", "method", "reference").Unique().Annotations(entsql.IndexWhere("status <> 'rejected'")),
	}
}
