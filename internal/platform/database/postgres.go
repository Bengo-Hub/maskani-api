package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/bengobox/maskani-api/internal/config"
)

// NewPool builds a pgx pool used for health checks and raw read queries.
func NewPool(ctx context.Context, cfg config.PostgresConfig) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse config: %w", err)
	}
	poolConfig.MaxConns = int32(cfg.MaxOpenConns)
	poolConfig.MinConns = 0
	poolConfig.MaxConnLifetime = cfg.ConnMaxLifetime
	// No startup RuntimeParams: the fleet PgBouncer accepts only extra_float_digits, search_path and
	// options (ignore_startup_parameters) and rejects any other startup parameter with FATAL 08P01,
	// which failed every connection and kept /readyz at 503 (2026-10-08).

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("postgres: create pool: %w", err)
	}
	// Best-effort session guardrail, the same as treasury-api and the rest of the fleet.
	if cfg.StatementTimeout > 0 {
		_, _ = pool.Exec(ctx, fmt.Sprintf("SET statement_timeout = '%dms'", cfg.StatementTimeout.Milliseconds()))
	}
	return pool, nil
}

// OpenSQL opens a database/sql handle for Ent with pool limits applied.
func OpenSQL(url string, cfg config.PostgresConfig) (*sql.DB, error) {
	db, err := sql.Open("pgx", url)
	if err != nil {
		return nil, fmt.Errorf("postgres: open: %w", err)
	}
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	db.SetConnMaxIdleTime(5 * time.Minute)
	return db, nil
}
