// Package tenantguard carries the tenant a piece of work runs for, so the Ent interceptor and hook
// on every tenant-owned schema can scope queries and stamp writes. It is the R1 isolation control
// in place of PostgreSQL row-level security (see docs/backlog.md).
//
// Rules:
//   - HTTP requests get the tenant from the signed token via With.
//   - Background jobs and event consumers either call With per tenant, or System when they
//     deliberately read across tenants (and then filter by tenant_id themselves).
//   - A query on a tenant-owned table with neither fails closed.
package tenantguard

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

type ctxKey int

const (
	tenantKey ctxKey = iota
	systemKey
)

// ErrNoTenant is returned when a tenant-owned table is touched without a tenant or system context.
var ErrNoTenant = errors.New("tenantguard: no tenant in context")

// ErrTenantMismatch is returned when a write carries a tenant_id other than the context tenant.
var ErrTenantMismatch = errors.New("tenantguard: tenant mismatch")

// With returns a context scoped to tenantID.
func With(ctx context.Context, tenantID uuid.UUID) context.Context {
	return context.WithValue(ctx, tenantKey, tenantID)
}

// System returns a context that bypasses the guard. Use only in jobs, consumers and seeders that
// filter by tenant themselves.
func System(ctx context.Context) context.Context {
	return context.WithValue(ctx, systemKey, true)
}

// TenantID returns the context tenant.
func TenantID(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(tenantKey).(uuid.UUID)
	return id, ok && id != uuid.Nil
}

// IsSystem reports whether the context bypasses the guard.
func IsSystem(ctx context.Context) bool {
	v, _ := ctx.Value(systemKey).(bool)
	return v
}

// MustTenant returns the context tenant or ErrNoTenant.
func MustTenant(ctx context.Context) (uuid.UUID, error) {
	if id, ok := TenantID(ctx); ok {
		return id, nil
	}
	return uuid.Nil, ErrNoTenant
}
