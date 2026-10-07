// Command migrate applies the versioned Ent/Atlas migrations for maskani-api.
package main

import (
	"context"
	"database/sql"
	"log"
	"os"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/schema"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/migrate"
)

// migrationLockKey is unique across the fleet (pos 727271001 to logistics 727271006).
const migrationLockKey = 727271007

func main() {
	dbURL := os.Getenv("POSTGRES_MIGRATE_URL")
	if dbURL == "" {
		dbURL = os.Getenv("POSTGRES_URL")
	}
	if dbURL == "" {
		log.Fatal("POSTGRES_URL or POSTGRES_MIGRATE_URL must be set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	db, err := sql.Open("pgx", dbURL)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	// Every replica runs this on startup. One pinned connection plus a session advisory lock means
	// only one pod migrates at a time; the others wait, then find nothing pending. This must use the
	// direct DSN: session locks do not survive PgBouncer transaction pooling.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(5 * time.Minute)

	if _, err := db.ExecContext(ctx, "SELECT pg_advisory_lock($1)", migrationLockKey); err != nil {
		log.Fatalf("acquire migration lock: %v", err)
	}
	unlock := func() {
		if _, err := db.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", migrationLockKey); err != nil {
			log.Printf("release migration lock: %v", err)
		}
	}

	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))

	// Drop options are explicit: without them a removed column or index in a schema struct is
	// silently left in the database.
	err = client.Schema.Create(ctx,
		schema.WithDir(migrate.Dir),
		schema.WithDropColumn(true),
		schema.WithDropIndex(true),
	)
	// Release the lock before closing: closing the client closes the shared connection.
	unlock()
	if err != nil {
		log.Fatalf("migrate: %v", err)
	}
	log.Println("migrations completed")
}
