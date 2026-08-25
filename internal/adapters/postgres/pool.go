// Package postgres connects HackWerk adapters to PostgreSQL.
package postgres

import (
	"context"
	"fmt"
	"time"

	"example.invalid/hackplan/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Open creates a lazy PostgreSQL pool with bounded connection settings.
// Call Ping explicitly when the caller requires startup connectivity.
func Open(ctx context.Context, cfg config.Database) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("postgres: parsing database configuration: %w", err)
	}
	poolConfig.MaxConns = cfg.MaxConnections
	poolConfig.MinConns = cfg.MinConnections
	poolConfig.MinIdleConns = cfg.MinConnections
	poolConfig.MaxConnLifetime = time.Hour
	poolConfig.MaxConnLifetimeJitter = 5 * time.Minute
	poolConfig.MaxConnIdleTime = 15 * time.Minute
	poolConfig.HealthCheckPeriod = time.Minute
	poolConfig.PingTimeout = cfg.ReadinessTimeout
	poolConfig.ConnConfig.ConnectTimeout = cfg.ConnectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("postgres: creating pool: %w", err)
	}
	return pool, nil
}

// Ping verifies database reachability within the configured readiness timeout.
func Ping(ctx context.Context, pool *pgxpool.Pool, timeout time.Duration) error {
	pingContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := pool.Ping(pingContext); err != nil {
		return fmt.Errorf("postgres: pinging database: %w", err)
	}
	return nil
}
