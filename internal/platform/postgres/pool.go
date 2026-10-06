// Package postgres owns PostgreSQL pool construction and lifecycle behavior.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/platform/config"
)

// Open creates a configured pool and verifies connectivity before returning it.
func Open(ctx context.Context, database config.DatabaseConfig) (*pgxpool.Pool, error) {
	if err := database.Validate(); err != nil {
		return nil, fmt.Errorf("invalid database configuration: %w", err)
	}
	poolConfiguration, err := poolConfig(database)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfiguration)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return pool, nil
}

func poolConfig(database config.DatabaseConfig) (*pgxpool.Config, error) {
	poolConfiguration, err := pgxpool.ParseConfig(database.URL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	poolConfiguration.MaxConns = database.MaxConns
	poolConfiguration.MinConns = database.MinConns
	poolConfiguration.MaxConnLifetime = database.MaxConnLifetime
	poolConfiguration.MaxConnIdleTime = database.MaxConnIdleTime
	poolConfiguration.HealthCheckPeriod = database.HealthCheckPeriod
	return poolConfiguration, nil
}
