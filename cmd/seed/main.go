// Command seed loads idempotent platform reference data: the RBAC catalogue and the default
// catalogues every tenant starts from. Safe on every deploy.
package main

import (
	"context"
	"log"
	"os"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/config"
	"github.com/bengobox/maskani-api/internal/ent"
	_ "github.com/bengobox/maskani-api/internal/ent/runtime"
	"github.com/bengobox/maskani-api/internal/modules/rbac"
	"github.com/bengobox/maskani-api/internal/modules/settings"
	"github.com/bengobox/maskani-api/internal/platform/database"
)

func main() {
	dsn := os.Getenv("POSTGRES_URL")
	if dsn == "" {
		dsn = "postgres://postgres:postgres@localhost:5432/maskani?sslmode=disable"
	}
	db, err := database.OpenSQL(dsn, config.PostgresConfig{MaxOpenConns: 2, MaxIdleConns: 1, ConnMaxLifetime: time.Minute})
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer client.Close()
	zl, _ := zap.NewProduction()
	defer zl.Sync()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := rbac.NewService(client, zl).Seed(ctx); err != nil {
		log.Fatalf("seed rbac: %v", err)
	}
	if err := settings.NewService(client, zl).SeedPlatformCatalog(ctx); err != nil {
		log.Fatalf("seed catalogues: %v", err)
	}
	log.Println("maskani seed complete")
}
