package database

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"sort"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/codeready-toolchain/tarsy/test/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigrations_ApplyAll applies all SQL migrations against a fresh schema
// and verifies the resulting tables and columns exist. This catches SQL
// errors, wrong type names, and missing dependencies in migration files.
func TestMigrations_ApplyAll(t *testing.T) {
	ctx := context.Background()
	db, dbName := setupMigrationTestDB(t)

	drv := entsql.OpenDB(dialect.Postgres, db)
	err := runMigrations(ctx, db, Config{Database: dbName}, drv)
	require.NoError(t, err, "all migrations should apply cleanly")

	// Spot-check key tables exist (migrations create tables in "public")
	tables := queryTables(t, db, "public")
	for _, expected := range []string{
		"alert_sessions",
		"agent_executions",
		"timeline_events",
		"stages",
		"llm_interactions",
	} {
		assert.Contains(t, tables, expected, "table %q should exist after migrations", expected)
	}

	// Verify columns from the latest migration (fallback fields)
	columns := queryColumns(t, db, "public", "agent_executions")
	assert.Contains(t, columns, "original_llm_provider",
		"original_llm_provider column should exist after migrations")
	assert.Contains(t, columns, "original_llm_backend",
		"original_llm_backend column should exist after migrations")
}

// TestMigrations_EntParity verifies the schema produced by SQL migrations
// matches the schema produced by Ent's auto-migration. Catches drift between
// the two, e.g. a migration adding a column that Ent doesn't know about.
func TestMigrations_EntParity(t *testing.T) {
	ctx := context.Background()

	// Side A: SQL migrations (in a fresh database, tables land in "public")
	dbMig, dbName := setupMigrationTestDB(t)
	drvMig := entsql.OpenDB(dialect.Postgres, dbMig)
	err := runMigrations(ctx, dbMig, Config{Database: dbName}, drvMig)
	require.NoError(t, err, "migrations should apply cleanly")
	err = CreatePartialUniqueIndexes(ctx, drvMig)
	require.NoError(t, err)

	// Side B: Ent auto-migration (in a per-test schema)
	entClient, dbEnt := util.SetupTestDatabase(t)
	schemaEnt := extractSchemaName(t, dbEnt)
	drvEnt := entsql.OpenDB(dialect.Postgres, dbEnt)
	err = CreateGINIndexes(ctx, drvEnt)
	require.NoError(t, err)
	err = CreatePartialUniqueIndexes(ctx, drvEnt)
	require.NoError(t, err)
	_ = entClient

	// Compare tables (migrations use "public" schema in the fresh DB)
	migTables := queryTables(t, dbMig, "public")
	entTables := queryTables(t, dbEnt, schemaEnt)

	// schema_migrations is created by golang-migrate, not Ent
	filtered := make([]string, 0, len(migTables))
	for _, tbl := range migTables {
		if tbl != "schema_migrations" {
			filtered = append(filtered, tbl)
		}
	}
	migTables = filtered

	sort.Strings(migTables)
	sort.Strings(entTables)
	assert.Equal(t, entTables, migTables, "migration and Ent should produce the same tables")

	// Compare columns for each shared table
	sharedTables := intersect(migTables, entTables)
	for _, table := range sharedTables {
		migCols := queryColumnTypes(t, dbMig, "public", table)
		entCols := queryColumnTypes(t, dbEnt, schemaEnt, table)
		assert.Equal(t, entCols, migCols,
			"column mismatch in table %q between migration and Ent schemas", table)
	}
}

// setupMigrationTestDB creates a fresh temporary database for migration testing.
// SQL migration files hard-code "public" as the schema, so per-schema isolation
// doesn't work — we need a separate database for each test.
func setupMigrationTestDB(t *testing.T) (*stdsql.DB, string) {
	t.Helper()
	ctx := context.Background()

	connStr := util.GetBaseConnectionString(t)
	dbName := "mig_" + util.GenerateSchemaName(t)
	// PostgreSQL identifiers are max 63 chars
	if len(dbName) > 63 {
		dbName = dbName[:63]
	}

	adminDB, err := stdsql.Open("pgx", connStr)
	require.NoError(t, err)

	_, err = adminDB.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE %s", dbName))
	require.NoError(t, err)
	_ = adminDB.Close()

	// Build connection string for the new database
	migConnStr := replaceDatabaseInConnString(connStr, dbName)
	db, err := stdsql.Open("pgx", migConnStr)
	require.NoError(t, err)
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)

	t.Cleanup(func() {
		_ = db.Close()
		// Reconnect to admin DB to drop the test database
		admin, err := stdsql.Open("pgx", connStr)
		if err != nil {
			t.Logf("Warning: failed to connect for cleanup: %v", err)
			return
		}
		defer admin.Close()
		_, _ = admin.ExecContext(ctx, fmt.Sprintf(
			"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s' AND pid <> pg_backend_pid()", dbName))
		_, _ = admin.ExecContext(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", dbName))
	})

	return db, dbName
}

// replaceDatabaseInConnString replaces the database name in a PostgreSQL
// connection string. Handles both URI format (postgresql://.../) and
// key-value format (dbname=...).
func replaceDatabaseInConnString(connStr, newDB string) string {
	// URI format: postgresql://user:pass@host:port/dbname?params
	if strings.HasPrefix(connStr, "postgres://") || strings.HasPrefix(connStr, "postgresql://") {
		// Find the path component (after host:port, before ?)
		schemeEnd := strings.Index(connStr, "://") + 3
		rest := connStr[schemeEnd:]
		// Find the slash after host:port
		slashIdx := strings.Index(rest, "/")
		if slashIdx == -1 {
			return connStr + "/" + newDB
		}
		afterSlash := rest[slashIdx+1:]
		qIdx := strings.Index(afterSlash, "?")
		if qIdx == -1 {
			return connStr[:schemeEnd] + rest[:slashIdx+1] + newDB
		}
		return connStr[:schemeEnd] + rest[:slashIdx+1] + newDB + afterSlash[qIdx:]
	}
	return connStr
}

// extractSchemaName reads the search_path from an existing connection to
// determine which schema SetupTestDatabase created.
func extractSchemaName(t *testing.T, db *stdsql.DB) string {
	t.Helper()
	var schema string
	err := db.QueryRowContext(context.Background(), "SHOW search_path").Scan(&schema)
	require.NoError(t, err)
	return schema
}

func queryTables(t *testing.T, db *stdsql.DB, schema string) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT table_name FROM information_schema.tables
		 WHERE table_schema = $1 AND table_type = 'BASE TABLE'`, schema)
	require.NoError(t, err)
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		tables = append(tables, name)
	}
	return tables
}

func queryColumns(t *testing.T, db *stdsql.DB, schema, table string) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT column_name FROM information_schema.columns
		 WHERE table_schema = $1 AND table_name = $2
		 ORDER BY ordinal_position`, schema, table)
	require.NoError(t, err)
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		cols = append(cols, name)
	}
	return cols
}

// columnInfo holds name and type for comparison.
type columnInfo struct {
	Name     string
	DataType string
	Nullable string
}

func queryColumnTypes(t *testing.T, db *stdsql.DB, schema, table string) []columnInfo {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT column_name, data_type, is_nullable
		 FROM information_schema.columns
		 WHERE table_schema = $1 AND table_name = $2
		 ORDER BY column_name`, schema, table)
	require.NoError(t, err)
	defer rows.Close()

	var cols []columnInfo
	for rows.Next() {
		var c columnInfo
		require.NoError(t, rows.Scan(&c.Name, &c.DataType, &c.Nullable))
		cols = append(cols, c)
	}
	return cols
}

func intersect(a, b []string) []string {
	set := make(map[string]bool, len(b))
	for _, s := range b {
		set[s] = true
	}
	var result []string
	for _, s := range a {
		if set[s] {
			result = append(result, s)
		}
	}
	sort.Strings(result)
	return result
}
