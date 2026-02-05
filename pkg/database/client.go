package database

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"time"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
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

// Client wraps Ent client and provides access to the underlying database
type Client struct {
	*ent.Client
	db *stdsql.DB
}

// DB returns the underlying database connection for health checks and direct queries
func (c *Client) DB() *stdsql.DB {
	return c.db
}

// NewClientFromEnt wraps an existing Ent client (useful for testing)
func NewClientFromEnt(entClient *ent.Client, db *stdsql.DB) *Client {
	return &Client{
		Client: entClient,
		db:     db,
	}
}

// NewClient creates a new database client with connection pooling and migrations
func NewClient(ctx context.Context, cfg Config) (*Client, error) {
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
	entClient := ent.NewClient(ent.Driver(drv))

	// Run migrations
	if err := runMigrations(ctx, db, cfg, drv); err != nil {
		entClient.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	// Wrap in our client type
	client := &Client{
		Client: entClient,
		db:     db,
	}

	return client, nil
}

// runMigrations runs database migrations using golang-migrate.
// This implements the versioned migration workflow:
//   1. Developer changes schema: Edit ent/schema/*.go
//   2. Generate migration: make migrate-create NAME=add_feature
//   3. Review & commit: Check SQL files, commit to git
//   4. Deploy: New code pushed
//   5. Auto-apply: App applies pending migrations on startup (this function)
//
// Migrations are applied sequentially from ent/migrate/migrations/ directory.
// Already-applied migrations are skipped automatically.
func runMigrations(ctx context.Context, db *stdsql.DB, cfg Config, drv *sql.Driver) error {
	// Create postgres driver instance for golang-migrate
	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("failed to create postgres driver: %w", err)
	}

	// Create migrate instance with migration files
	m, err := migrate.NewWithDatabaseInstance(
		"file://ent/migrate/migrations",
		cfg.Database, // database name
		driver,
	)
	if err != nil {
		return fmt.Errorf("failed to create migrate instance: %w", err)
	}

	// Apply all pending migrations
	err = m.Up()
	if err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("failed to apply migrations: %w", err)
	}

	// Create GIN indexes (custom SQL not handled by Ent schema)
	if err := CreateGINIndexes(ctx, drv); err != nil {
		return fmt.Errorf("failed to create GIN indexes: %w", err)
	}

	return nil
}
