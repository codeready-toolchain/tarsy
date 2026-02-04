package database

import (
	"context"
	"fmt"
	"time"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"github.com/codeready-toolchain/tarsy/ent"
	_ "github.com/lib/pq"
)

// Config holds database configuration
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
	SSLMode  string

	// Connection pool settings
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// NewClient creates a new Ent database client with connection pooling and migrations
func NewClient(ctx context.Context, cfg Config) (*ent.Client, error) {
	dsn := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database, cfg.SSLMode,
	)

	// Open connection with driver
	drv, err := sql.Open(dialect.Postgres, dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database driver: %w", err)
	}

	// Configure connection pool
	db := drv.DB()
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	// Test connection
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Create Ent client with configured driver
	client := ent.NewClient(ent.Driver(drv))

	// Run migrations
	if err := runMigrations(ctx, client); err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return client, nil
}

// runMigrations runs database migrations
func runMigrations(ctx context.Context, client *ent.Client) error {
	// Create schema with auto-migration
	// In production, use versioned migrations instead
	if err := client.Schema.Create(ctx); err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}
	return nil
}
