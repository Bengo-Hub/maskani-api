package tenantguard

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestWithDropsSystem: narrowing a system context to one tenant must turn the guard back on.
func TestWithDropsSystem(t *testing.T) {
	sys := System(context.Background())
	if !IsSystem(sys) {
		t.Fatal("System context not flagged")
	}
	id := uuid.New()
	scoped := With(sys, id)
	if IsSystem(scoped) {
		t.Fatal("With kept the system flag, so the tenant scope would be ignored")
	}
	if got, ok := TenantID(scoped); !ok || got != id {
		t.Fatalf("tenant %v %v, want %v", got, ok, id)
	}
	if !IsSystem(System(scoped)) {
		t.Fatal("System on a scoped context must bypass again")
	}
}
