// Package util provides test utilities and helper functions for database testing.
package util

import (
	"context"
	"crypto/rand"
	stdsql "database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

var (
	// Shared connection string for all tests in local dev
	sharedConnStr string
	containerOnce sync.Once
	containerErr  error
)

// SetupTestDatabase creates a test database and returns the raw components.
// Both CI and local dev use per-test schemas for isolation and scalability.
// - CI: Connects to external PostgreSQL service container
// - Local: Uses a shared testcontainer (started once per package)
// Returns the ent client and database connection for wrapping by the caller.
func SetupTestDatabase(t *testing.T) (*ent.Client, *stdsql.DB) {
	ctx := context.Background()

	// Get connection string (from CI env var or shared container)
	connStr := getOrCreateSharedDatabase(t)

	// Generate unique schema name for this test
	schemaName := GenerateSchemaName(t)

	// Connect to the base database to create the schema
	db, err := stdsql.Open("pgx", connStr)
	require.NoError(t, err)

	// Create the test schema
	_, err = db.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA %s", schemaName))
	require.NoError(t, err)

	t.Logf("Created test schema: %s", schemaName)

	// Close the initial connection
	_ = db.Close()

	// Reconnect with search_path set in connection string for all pooled connections
	connStrWithSchema := AddSearchPathToConnString(connStr, schemaName)
	db, err = stdsql.Open("pgx", connStrWithSchema)
	require.NoError(t, err)

	// Configure connection pool for tests
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)

	// Create Ent driver with search_path already set for all connections
	drv := entsql.OpenDB(dialect.Postgres, db)

	// Create Ent client
	entClient := ent.NewClient(ent.Driver(drv))

	// Run migrations in the test schema
	err = entClient.Schema.Create(ctx)
	require.NoError(t, err)

	// Cleanup: drop the schema when test completes
	t.Cleanup(func() {
		// Drop schema before closing connections
		_, err := db.ExecContext(context.Background(), fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schemaName))
		if err != nil {
			t.Logf("Warning: failed to drop schema %s: %v", schemaName, err)
		}
		_ = entClient.Close()
		_ = db.Close()
	})

	return entClient, db
}

// GetBaseConnectionString returns the base PostgreSQL connection string
// (without schema search_path). Used by integration tests that need a raw
// connection string for dedicated connections, e.g. NotifyListener's pgx.Conn.
func GetBaseConnectionString(t *testing.T) string {
	return getOrCreateSharedDatabase(t)
}

// getOrCreateSharedDatabase returns a connection string to the shared database.
// In CI, uses CI_DATABASE_URL. In local dev, creates a shared testcontainer once.
func getOrCreateSharedDatabase(t *testing.T) string {
	// Check if we're in CI with an external database
	if ciDatabaseURL := os.Getenv("CI_DATABASE_URL"); ciDatabaseURL != "" {
		t.Log("Using external PostgreSQL from CI_DATABASE_URL")
		return ciDatabaseURL
	}

	// Local dev: ensure shared container is started (once per package)
	containerOnce.Do(func() {
		ctx := context.Background()
		t.Log("Starting shared PostgreSQL testcontainer for all tests")

		// Resolve init script path relative to this source file (works from any package)
		initScriptPath := resolveInitScriptPath()

		pgContainer, err := postgres.Run(ctx,
			"postgres:17-alpine",
			postgres.WithDatabase("test"),
			postgres.WithUsername("test"),
			postgres.WithPassword("test"),
			postgres.WithInitScripts(initScriptPath),
			testcontainers.WithWaitStrategy(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(30*time.Second)),
		)
		if err != nil {
			containerErr = fmt.Errorf("failed to start postgres container: %w", err)
			return
		}

		// Get connection string
		connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			containerErr = fmt.Errorf("failed to get connection string: %w", err)
			return
		}

		sharedConnStr = connStr
		t.Logf("Shared container ready: %s", sharedConnStr)
	})

	require.NoError(t, containerErr, "Failed to setup shared test container")
	return sharedConnStr
}

// GenerateSchemaName creates a unique, PostgreSQL-safe schema name for the test.
// Format: test_<sanitized_test_name>_<random_hex>
func GenerateSchemaName(t *testing.T) string {
	// Get test name and sanitize it (lowercase, replace invalid chars with _)
	testName := strings.ToLower(t.Name())
	testName = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, testName)

	// Limit length to avoid PostgreSQL's 63 char identifier limit
	if len(testName) > 40 {
		testName = testName[:40]
	}

	// Add random suffix for uniqueness
	randomBytes := make([]byte, 4)
	_, err := rand.Read(randomBytes)
	if err != nil {
		// crypto/rand.Read should never fail, but handle it defensively
		t.Fatalf("failed to generate random bytes for schema name: %v", err)
	}
	randomHex := hex.EncodeToString(randomBytes)

	return fmt.Sprintf("test_%s_%s", testName, randomHex)
}

// AddSearchPathToConnString appends search_path parameter to a PostgreSQL connection string.
// This ensures all connections in the pool use the specified schema.
func AddSearchPathToConnString(connStr, schemaName string) string {
	// Add search_path as a connection parameter
	separator := "?"
	if strings.Contains(connStr, "?") {
		separator = "&"
	}
	return fmt.Sprintf("%s%ssearch_path=%s", connStr, separator, schemaName)
}

// resolveInitScriptPath returns the absolute path to the postgres init script.
// Uses runtime.Caller to resolve relative to this source file, so it works
// regardless of which package's test is running.
func resolveInitScriptPath() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("resolveInitScriptPath: runtime.Caller(0) failed")
	}
	projectRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile))) // test/util/ → test/ → project root
	return filepath.Join(projectRoot, "deploy", "postgres-init", "01-init.sql")
}
