package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// approvalModules are the workflows that run on the central approvals engine, the same engine
// shape as treasury-api and inventory-api (rules with ordered role steps, one request per object).
var approvalModules = []string{"credit_note", "adjustment", "manual_payment", "restructure", "vendor_bill", "refund",
	"work_order_quote", "deposit_deduction", "remittance", "write_off"}

// ApprovalRule says who approves a module's objects in an amount band: an ordered list of steps,
// each naming the role (or, for built-in defaults, the permission) allowed to act on it. Steps are
// kept as JSON on the rule: [{sequence, name, approver_role, permission}].
type ApprovalRule struct{ ent.Schema }

func (ApprovalRule) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (ApprovalRule) Fields() []ent.Field {
	return []ent.Field{
		field.Enum("module").Values(approvalModules...),
		field.String("name").Default(""),
		money("min_amount"),
		optMoney("max_amount"),
		field.JSON("steps", []map[string]any{}),
		field.Bool("require_otp").Default(false),
		field.Bool("is_active").Default(true),
	}
}

func (ApprovalRule) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "module", "is_active")}
}

// ApprovalRequest is one object's live approval: the steps copied from the matching rule (or the
// module default) with each step's decision, kept as JSON in `actions`
// [{sequence, name, approver_role, permission, status, acted_by, acted_by_name, acted_at, comment}].
// A step is claimed with a compare-and-set on (status, current_sequence), so two approvers at once
// cannot both win. Metadata keeps what the inbox shows (account_ref, unit_code, label).
type ApprovalRequest struct{ ent.Schema }

func (ApprovalRequest) Mixin() []ent.Mixin { return []ent.Mixin{TenantMixin{}} }

func (ApprovalRequest) Fields() []ent.Field {
	return []ent.Field{
		field.Enum("module").Values(approvalModules...),
		field.UUID("object_id", uuid.UUID{}),
		field.String("object_reference").Default(""),
		money("amount"),
		field.UUID("property_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("rule_id", uuid.UUID{}).Optional().Nillable(),
		field.Enum("status").Values("pending", "approved", "rejected", "cancelled").Default("pending"),
		field.Int("current_sequence").Default(1),
		// CurrentApprover is who may act now: a role code, or "perm:<code>" for a permission step,
		// so the inbox can ask "waiting for me" in SQL.
		field.String("current_approver").Default(""),
		field.JSON("actions", []map[string]any{}),
		field.UUID("submitted_by", uuid.UUID{}).Optional().Nillable(),
		field.String("submitted_by_name").Default(""),
		field.Time("decided_at").Optional().Nillable(),
	}
}

func (ApprovalRequest) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "object_id", "created_at"),
		index.Fields("tenant_id", "status", "created_at"),
		index.Fields("tenant_id", "property_id", "status", "created_at"),
		index.Fields("tenant_id", "status", "current_approver"),
	}
}
