package database

import (
	"context"
	"os"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/pkg/database"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// NewTestClient creates a test database client.
// In CI (when CI_DATABASE_URL is set): connects to external PostgreSQL service container.
// In local dev: spins up a testcontainer with PostgreSQL.
// The container/connection is automatically cleaned up when the test ends.
func NewTestClient(t *testing.T) *database.Client {
	ctx := context.Background()

	// Check if we're in CI with an external database
	ciDatabaseURL := os.Getenv("CI_DATABASE_URL")
	
	var connStr string
	
	if ciDatabaseURL != "" {
		// CI mode: use external PostgreSQL service container
		t.Log("Using external PostgreSQL from CI_DATABASE_URL")
		connStr = ciDatabaseURL
	} else {
		// Local dev mode: use testcontainers
		t.Log("Using testcontainers for PostgreSQL")
		pgContainer, err := postgres.Run(ctx,
			"postgres:16-alpine",
			postgres.WithDatabase("test"),
			postgres.WithUsername("test"),
			postgres.WithPassword("test"),
			postgres.WithInitScripts("../../deploy/postgres-init/01-init.sql"),
			testcontainers.WithWaitStrategy(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(30*time.Second)),
		)
		require.NoError(t, err)

		t.Cleanup(func() {
			if err := testcontainers.TerminateContainer(pgContainer); err != nil {
				t.Logf("failed to terminate container: %v", err)
			}
		})

		// Get connection string from container
		var err2 error
		connStr, err2 = pgContainer.ConnectionString(ctx, "sslmode=disable")
		require.NoError(t, err2)
	}

	// Open connection with driver
	drv, err := sql.Open(dialect.Postgres, connStr)
	require.NoError(t, err)

	// Configure connection pool for tests
	db := drv.DB()
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)

	// Create Ent client
	entClient := ent.NewClient(ent.Driver(drv))

	// Run migrations (auto-migration for tests)
	// In production, this would use versioned migrations from ent/migrate/migrations/
	err = entClient.Schema.Create(ctx)
	require.NoError(t, err)

	// Create GIN indexes
	err = database.CreateGINIndexes(ctx, drv)
	require.NoError(t, err)

	// Wrap in our client type
	client := database.NewClientFromEnt(entClient, db)

	t.Cleanup(func() {
		client.Close()
	})

	return client
}
