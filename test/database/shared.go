package database

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/pkg/database"
	"github.com/codeready-toolchain/tarsy/test/util"
	"github.com/stretchr/testify/require"
)

// SharedTestDB creates a single PostgreSQL schema that can be shared by
// multiple test replicas. Each replica gets its own connection pool via
// NewClient, but all pools point to the same schema — enabling cross-replica
// tests that exercise PostgreSQL NOTIFY/LISTEN event delivery.
type SharedTestDB struct {
	connStrWithSchema string
	baseConnStr       string
	schemaName        string
}

// NewSharedTestDB creates a shared test schema, runs migrations and GIN
// indexes once, and registers t.Cleanup to drop the schema.
// Call NewClient to create independent database clients for each replica.
func NewSharedTestDB(t *testing.T) *SharedTestDB {
	t.Helper()
	ctx := context.Background()

	baseConnStr := util.GetBaseConnectionString(t)
	schemaName := util.GenerateSchemaName(t)

	// Create the schema.
	db, err := stdsql.Open("pgx", baseConnStr)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA %s", schemaName))
	require.NoError(t, err)
	t.Logf("SharedTestDB: created schema %s", schemaName)
	_ = db.Close()

	// Connect with search_path and run migrations once.
	connStrWithSchema := util.AddSearchPathToConnString(baseConnStr, schemaName)
	db, err = stdsql.Open("pgx", connStrWithSchema)
	require.NoError(t, err)
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)

	drv := entsql.OpenDB(dialect.Postgres, db)
	entClient := ent.NewClient(ent.Driver(drv))

	err = entClient.Schema.Create(ctx)
	require.NoError(t, err)

	err = database.CreateGINIndexes(ctx, drv)
	require.NoError(t, err)
	err = database.CreatePartialUniqueIndexes(ctx, drv)
	require.NoError(t, err)

	// Close the migration client — each replica creates its own.
	_ = entClient.Close()
	_ = db.Close()

	s := &SharedTestDB{
		connStrWithSchema: connStrWithSchema,
		baseConnStr:       baseConnStr,
		schemaName:        schemaName,
	}

	// Drop the schema after all replicas have shut down (LIFO order
	// guarantees TestApp cleanups run before this one).
	t.Cleanup(func() {
		cleanDB, err := stdsql.Open("pgx", baseConnStr)
		if err != nil {
			t.Logf("SharedTestDB: warning: could not connect to drop schema %s: %v", schemaName, err)
			return
		}
		defer func() { _ = cleanDB.Close() }()
		_, err = cleanDB.ExecContext(context.Background(), fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schemaName))
		if err != nil {
			t.Logf("SharedTestDB: warning: failed to drop schema %s: %v", schemaName, err)
		}
	})

	return s
}

// NewClient creates an independent *database.Client backed by a fresh
// connection pool to the shared schema. Each client has its own pool so
// replicas can be shut down independently without races.
// The client's connections are closed via t.Cleanup.
func (s *SharedTestDB) NewClient(t *testing.T) *database.Client {
	t.Helper()

	db, err := stdsql.Open("pgx", s.connStrWithSchema)
	require.NoError(t, err)
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)

	drv := entsql.OpenDB(dialect.Postgres, db)
	entClient := ent.NewClient(ent.Driver(drv))
	client := database.NewClientFromEnt(entClient, db)

	t.Cleanup(func() {
		_ = entClient.Close()
		_ = db.Close()
	})

	return client
}
