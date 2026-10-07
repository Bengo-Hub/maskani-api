package schema

import (
	"context"
	"fmt"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/sql"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/bengobox/maskani-api/internal/ent/intercept"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// TenantMixin gives a domain table its id, tenant_id, timestamps and metadata, and installs the
// tenant guard: every query is filtered to the context tenant and every write is stamped and
// checked. A query with no tenant (and no explicit system context) fails closed.
type TenantMixin struct{ mixin.Schema }

func (TenantMixin) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.UUID("tenant_id", uuid.UUID{}).Immutable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
		field.JSON("metadata", map[string]any{}).Optional(),
	}
}

type wherePer interface {
	WhereP(...func(*sql.Selector))
}

// Interceptors scope every query and graph traversal on the table to the context tenant. The
// generated intercept package imports the generated ent package but not this schema package, so
// there is no import cycle (only ent/runtime imports the schema).
func (TenantMixin) Interceptors() []ent.Interceptor {
	return []ent.Interceptor{
		intercept.TraverseFunc(func(ctx context.Context, q intercept.Query) error {
			if tenantguard.IsSystem(ctx) {
				return nil
			}
			id, ok := tenantguard.TenantID(ctx)
			if !ok {
				return tenantguard.ErrNoTenant
			}
			q.WhereP(sql.FieldEQ("tenant_id", id))
			return nil
		}),
	}
}

type tenantSetter interface {
	SetTenantID(uuid.UUID)
	TenantID() (uuid.UUID, bool)
}

func (TenantMixin) Hooks() []ent.Hook {
	return []ent.Hook{
		func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
				if tenantguard.IsSystem(ctx) {
					return next.Mutate(ctx, m)
				}
				id, ok := tenantguard.TenantID(ctx)
				if !ok {
					return nil, tenantguard.ErrNoTenant
				}
				if m.Op().Is(ent.OpCreate) {
					ts, ok := m.(tenantSetter)
					if !ok {
						return nil, fmt.Errorf("tenantguard: %T has no tenant_id", m)
					}
					if cur, set := ts.TenantID(); set && cur != id {
						return nil, tenantguard.ErrTenantMismatch
					}
					ts.SetTenantID(id)
					return next.Mutate(ctx, m)
				}
				if w, ok := m.(wherePer); ok {
					w.WhereP(sql.FieldEQ("tenant_id", id))
				}
				return next.Mutate(ctx, m)
			})
		},
	}
}

// money is a numeric(18,2) amount mapped to decimal.Decimal, with an optional column comment.
func money(name string, comment ...string) ent.Field {
	b := field.Float(name).
		GoType(decimal.Decimal{}).
		SchemaType(map[string]string{"postgres": "numeric(18,2)"}).
		DefaultFunc(func() decimal.Decimal { return decimal.Zero })
	if len(comment) > 0 {
		return b.Comment(comment[0])
	}
	return b
}

// optMoney is a nullable money column.
func optMoney(name string) ent.Field {
	return field.Float(name).
		GoType(decimal.Decimal{}).
		SchemaType(map[string]string{"postgres": "numeric(18,2)"}).
		Optional().Nillable()
}

// qty is a numeric(18,3) quantity (meter readings, consumption, shares, entitlements).
func qty(name string, comment ...string) ent.Field {
	b := field.Float(name).
		GoType(decimal.Decimal{}).
		SchemaType(map[string]string{"postgres": "numeric(18,3)"}).
		DefaultFunc(func() decimal.Decimal { return decimal.Zero })
	if len(comment) > 0 {
		return b.Comment(comment[0])
	}
	return b
}

// optQty is a nullable quantity column.
func optQty(name string) ent.Field {
	return field.Float(name).
		GoType(decimal.Decimal{}).
		SchemaType(map[string]string{"postgres": "numeric(18,3)"}).
		Optional().Nillable()
}

// customFields is the per-tenant custom field values bag (validated against custom_field_defs).
func customFields() ent.Field {
	return field.JSON("custom_fields", map[string]any{}).Optional()
}

// createdBy records the auth user who created the record.
func createdBy() ent.Field {
	return field.UUID("created_by", uuid.UUID{}).Optional().Nillable()
}
