package settings

import (
	"context"
	"os"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/config"
	"github.com/bengobox/maskani-api/internal/ent"
	_ "github.com/bengobox/maskani-api/internal/ent/runtime"
	"github.com/bengobox/maskani-api/internal/platform/database"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// TestApprovalRuleBands checks that bands for one action never overlap, while another action's
// may. Needs SETTINGS_TEST_POSTGRES_URL (a scratch database).
func TestApprovalRuleBands(t *testing.T) {
	dsn := os.Getenv("SETTINGS_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("SETTINGS_TEST_POSTGRES_URL not set")
	}
	db, err := database.OpenSQL(dsn, config.PostgresConfig{MaxOpenConns: 2, MaxIdleConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer client.Close()
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := tenantguard.With(context.Background(), uuid.New())
	s := &Service{client: client, log: zap.NewNop()}
	d := func(n int64) decimal.Decimal { return decimal.NewFromInt(n) }
	top := d(10000)
	if _, err := s.CreateApprovalRule(ctx, ApprovalRuleInput{Action: "credit_note", MinAmount: d(0), MaxAmount: &top, Levels: 1}); err != nil {
		t.Fatal(err)
	}
	high, err := s.CreateApprovalRule(ctx, ApprovalRuleInput{Action: "credit_note", MinAmount: d(10001), Levels: 2, ApproverRoles: []string{"tenant_admin"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateApprovalRule(ctx, ApprovalRuleInput{Action: "credit_note", MinAmount: d(5000), Levels: 1}); err == nil {
		t.Fatal("an overlapping band should be refused")
	}
	if _, err := s.CreateApprovalRule(ctx, ApprovalRuleInput{Action: "adjustment", MinAmount: d(0), Levels: 1}); err != nil {
		t.Fatalf("another action may use the same band: %v", err)
	}
	if _, err := s.UpdateApprovalRule(ctx, high.ID, ApprovalRuleInput{Action: "credit_note", MinAmount: d(10001), Levels: 4}); err == nil {
		t.Fatal("more than 3 levels should be refused")
	}
	if _, err := s.UpdateApprovalRule(ctx, high.ID, ApprovalRuleInput{Action: "credit_note", MinAmount: d(20000), Levels: 3}); err != nil {
		t.Fatalf("moving its own band should not clash with itself: %v", err)
	}
}
