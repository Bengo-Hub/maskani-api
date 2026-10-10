package register

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

func TestCleanPhotos(t *testing.T) {
	tid := uuid.New()
	ctx := tenantguard.With(context.Background(), tid)
	key := "tenants/" + tid.String() + "/units/2026-10/a.jpg"
	got, err := cleanPhotos(ctx, "units", []string{"https://api.example/media/" + key + "?exp=1&sig=x", key})
	if err != nil || len(got) != 1 || got[0] != key {
		t.Fatalf("signed link and repeat: %v %v", got, err)
	}
	if _, err := cleanPhotos(ctx, "units", []string{"tenants/" + uuid.NewString() + "/units/2026-10/a.jpg"}); err == nil {
		t.Fatal("another tenant's key should be refused")
	}
	if _, err := cleanPhotos(ctx, "properties", []string{key}); err == nil {
		t.Fatal("a unit photo should not go on a property")
	}
	if _, err := cleanPhotos(context.Background(), "units", []string{key}); err == nil {
		t.Fatal("no tenant should be refused")
	}
}
