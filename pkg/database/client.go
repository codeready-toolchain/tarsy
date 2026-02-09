// Package database provides PostgreSQL database client and migration utilities.
package database

import (
	"context"
	stdsql "database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib" // Register pgx driver for database/sql
)

//go:embed migrations
var migrationsFS embed.FS

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
	// Build pgx-compatible connection string
	dsn := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database, cfg.SSLMode,
	)

	// Open database connection using pgx driver
	db, err := stdsql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	// Test connection
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Create Ent driver from existing database connection
	// Use dialect.Postgres for Ent compatibility while pgx handles the actual connection
	drv := entsql.OpenDB(dialect.Postgres, db)

	// Create Ent client with configured driver
	entClient := ent.NewClient(ent.Driver(drv))

	// Run migrations
	if err := runMigrations(ctx, db, cfg, drv); err != nil {
		_ = entClient.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	// Wrap in our client type
	client := &Client{
		Client: entClient,
		db:     db,
	}

	return client, nil
}

// runMigrations runs database migrations using golang-migrate with embedded migration files.
//
// Migration files are embedded into the binary using go:embed, ensuring they're available
// in production deployments without requiring external files.
//
// Migration workflow:
//  1. Developer changes schema: Edit ent/schema/*.go
//  2. Generate migration: make migrate-create NAME=add_feature
//  3. Migrations saved to pkg/database/migrations/*.sql
//  4. Files embedded into binary at compile time
//  5. Review & commit: Check SQL files, commit to git
//  6. Deploy: Build binary (migrations embedded automatically)
//  7. Auto-apply: App applies pending migrations on startup (this function)
func runMigrations(ctx context.Context, db *stdsql.DB, cfg Config, drv *entsql.Driver) error {
	// Check if embedded migrations exist
	hasMigrations, err := hasEmbeddedMigrations()
	if err != nil {
		return fmt.Errorf("failed to check embedded migrations: %w", err)
	}

	if !hasMigrations {
		return fmt.Errorf("no embedded migration files found — binary may be built incorrectly")
	}

	// Use golang-migrate with embedded migrations
	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("failed to create postgres driver: %w", err)
	}

	// Create source from embedded FS
	sourceDriver, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("failed to create migration source: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", sourceDriver, cfg.Database, driver)
	if err != nil {
		return fmt.Errorf("failed to create migrate instance: %w", err)
	}

	// Apply all pending migrations
	err = m.Up()
	if err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("failed to apply migrations: %w", err)
	}

	// Close only the migration source driver. We must NOT call m.Close() because
	// that also closes the database driver, which calls db.Close() on the shared
	// *sql.DB passed via postgres.WithInstance() — breaking the Ent client.
	if err := sourceDriver.Close(); err != nil {
		return fmt.Errorf("failed to close migration source: %w", err)
	}

	// Create GIN indexes (custom SQL not handled by Ent schema)
	if err := CreateGINIndexes(ctx, drv); err != nil {
		return fmt.Errorf("failed to create GIN indexes: %w", err)
	}

	return nil
}

// hasEmbeddedMigrations checks if the embedded FS contains any .sql migration files
func hasEmbeddedMigrations() (bool, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		// If the migrations directory doesn't exist in the embed, no migrations
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("failed to read embedded migrations: %w", err)
	}

	// Check if there are any .sql files
	for _, entry := range entries {
		if !entry.IsDir() && len(entry.Name()) > 4 && entry.Name()[len(entry.Name())-4:] == ".sql" {
			return true, nil
		}
	}

	return false, nil
}
