package database

import (
	"context"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/codeready-toolchain/tarsy/pkg/database"
	"github.com/codeready-toolchain/tarsy/test/util"
	"github.com/stretchr/testify/require"
)

// NewTestClient creates a test database client.
// In CI (when CI_DATABASE_URL is set): connects to external PostgreSQL service container.
// In local dev: spins up a testcontainer with PostgreSQL.
// The container/connection is automatically cleaned up when the test ends.
func NewTestClient(t *testing.T) *database.Client {
	ctx := context.Background()

	// Use shared test database setup
	entClient, db := util.SetupTestDatabase(t)

	// Get the driver for GIN index creation
	drv := entsql.OpenDB(dialect.Postgres, db)

	// Create GIN indexes
	err := database.CreateGINIndexes(ctx, drv)
	require.NoError(t, err)

	// Wrap in our client type
	// Note: cleanup (schema drop and connection close) is handled by SetupTestDatabase
	client := database.NewClientFromEnt(entClient, db)

	return client
}
