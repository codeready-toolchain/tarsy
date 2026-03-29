package database

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/codeready-toolchain/tarsy/test/util"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
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
		"investigation_memories",
		"alert_session_injected_memories",
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

	// Compare columns for each shared table.
	// The investigation_memories.embedding column is raw SQL (pgvector type),
	// not managed by Ent, so we exclude it from the parity comparison.
	sharedTables := intersect(migTables, entTables)
	for _, table := range sharedTables {
		migCols := queryColumnTypes(t, dbMig, "public", table)
		entCols := queryColumnTypes(t, dbEnt, schemaEnt, table)

		switch table {
		case "investigation_memories":
			migCols = filterColumns(migCols, "embedding", "search_vector")
		case "alert_sessions":
			migCols = filterColumns(migCols, "search_vector")
		}

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
		schemeEnd := strings.Index(connStr, "://") + 3
		rest := connStr[schemeEnd:]
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

	// Key-value format: host=localhost user=test dbname=olddb ...
	tokens := strings.Fields(connStr)
	found := false
	for i, tok := range tokens {
		if strings.HasPrefix(tok, "dbname=") {
			tokens[i] = "dbname=" + newDB
			found = true
			break
		}
	}
	if !found {
		tokens = append(tokens, "dbname="+newDB)
	}
	return strings.Join(tokens, " ")
}

// extractSchemaName reads the search_path from an existing connection to
// determine which schema SetupTestDatabase created.
func extractSchemaName(t *testing.T, db *stdsql.DB) string {
	t.Helper()
	var schema string
	err := db.QueryRowContext(context.Background(), "SELECT current_schema()").Scan(&schema)
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
	require.NoError(t, rows.Err())
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
	require.NoError(t, rows.Err())
	return cols
}

// columnInfo holds name and type for comparison.
type columnInfo struct {
	Name     string
	DataType string
	Nullable string
	UdtName  string
}

func queryColumnTypes(t *testing.T, db *stdsql.DB, schema, table string) []columnInfo {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT column_name, data_type, is_nullable, udt_name
		 FROM information_schema.columns
		 WHERE table_schema = $1 AND table_name = $2
		 ORDER BY column_name`, schema, table)
	require.NoError(t, err)
	defer rows.Close()

	var cols []columnInfo
	for rows.Next() {
		var c columnInfo
		require.NoError(t, rows.Scan(&c.Name, &c.DataType, &c.Nullable, &c.UdtName))
		cols = append(cols, c)
	}
	require.NoError(t, rows.Err())
	return cols
}

func filterColumns(cols []columnInfo, exclude ...string) []columnInfo {
	excludeSet := make(map[string]bool, len(exclude))
	for _, name := range exclude {
		excludeSet[name] = true
	}
	var result []columnInfo
	for _, c := range cols {
		if !excludeSet[c.Name] {
			result = append(result, c)
		}
	}
	return result
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

// TestMigrations_DirtyRecovery verifies that runMigrations auto-recovers when
// the database is left in a dirty state by a previous failed deploy. It applies
// all-but-last migration, simulates a dirty marker for the last one, then
// confirms runMigrations recovers and applies it cleanly.
func TestMigrations_DirtyRecovery(t *testing.T) {
	ctx := context.Background()
	db, dbName := setupMigrationTestDB(t)

	total := countUpMigrations(t)
	require.Greater(t, total, 1, "need at least 2 migrations to test recovery")

	// Apply all but the last migration via a manual migrate instance.
	pgDriver, err := postgres.WithInstance(db, &postgres.Config{})
	require.NoError(t, err)
	srcDriver, err := iofs.New(migrationsFS, "migrations")
	require.NoError(t, err)
	m, err := migrate.NewWithInstance("iofs", srcDriver, dbName, pgDriver)
	require.NoError(t, err)

	err = m.Steps(total - 1)
	require.NoError(t, err)

	prevVersion, dirty, err := m.Version()
	require.NoError(t, err)
	require.False(t, dirty)

	latestVersion := latestMigrationVersion(t)
	require.Greater(t, latestVersion, prevVersion)

	// Simulate a dirty state: a previous deploy attempted the latest migration
	// but its transactional DDL rolled back. schema_migrations is left at the
	// latest version with dirty=true, while the actual schema is at prevVersion.
	_, err = db.ExecContext(ctx,
		"UPDATE schema_migrations SET version = $1, dirty = true", latestVersion)
	require.NoError(t, err)

	_ = srcDriver.Close()

	// Run the full migration flow — auto-recovery should detect dirty,
	// roll back to prevVersion, and re-apply the latest migration cleanly.
	drv := entsql.OpenDB(dialect.Postgres, db)
	err = runMigrations(ctx, db, Config{Database: dbName}, drv)
	require.NoError(t, err)

	var recoveredVersion uint
	var recoveredDirty bool
	err = db.QueryRowContext(ctx,
		"SELECT version, dirty FROM schema_migrations").Scan(&recoveredVersion, &recoveredDirty)
	require.NoError(t, err)
	assert.Equal(t, latestVersion, recoveredVersion)
	assert.False(t, recoveredDirty)
}

func countUpMigrations(t *testing.T) int {
	t.Helper()
	entries, err := migrationsFS.ReadDir("migrations")
	require.NoError(t, err)
	count := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".up.sql") {
			count++
		}
	}
	return count
}

func latestMigrationVersion(t *testing.T) uint {
	t.Helper()
	entries, err := migrationsFS.ReadDir("migrations")
	require.NoError(t, err)
	var latest uint64
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		parts := strings.SplitN(entry.Name(), "_", 2)
		v, err := strconv.ParseUint(parts[0], 10, 64)
		require.NoError(t, err)
		if v > latest {
			latest = v
		}
	}
	require.Greater(t, latest, uint64(0))
	return uint(latest)
}
