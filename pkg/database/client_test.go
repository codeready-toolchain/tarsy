package database

import (
	"context"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// newTestClient creates a test database client inline (avoiding import cycle with test/database)
func newTestClient(t *testing.T) *Client {
	ctx := context.Background()

	// Start PostgreSQL container
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

	// Get connection string
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

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
	err = entClient.Schema.Create(ctx)
	require.NoError(t, err)

	// Create GIN indexes
	err = CreateGINIndexes(ctx, drv)
	require.NoError(t, err)

	// Wrap in our client type
	client := NewClientFromEnt(entClient, db)

	t.Cleanup(func() {
		client.Close()
	})

	return client
}

func TestDatabaseClient_ConnectionPool(t *testing.T) {
	client := newTestClient(t)
	ctx := context.Background()

	// Test basic connectivity
	err := client.DB().PingContext(ctx)
	require.NoError(t, err)

	// Test health check
	health, err := Health(ctx, client.DB())
	require.NoError(t, err)
	assert.Equal(t, "healthy", health.Status)
	assert.Greater(t, health.MaxOpenConns, 0)
}

func TestFullTextSearch(t *testing.T) {
	client := newTestClient(t)
	ctx := context.Background()

	// Create test sessions
	session1, err := client.AlertSession.Create().
		SetID("test-1").
		SetAlertData("Critical error in production cluster with pod failures").
		SetAgentType("kubernetes").
		SetChainID("k8s-analysis").
		Save(ctx)
	require.NoError(t, err)

	session2, err := client.AlertSession.Create().
		SetID("test-2").
		SetAlertData("Warning: high memory usage detected").
		SetAgentType("kubernetes").
		SetChainID("k8s-analysis").
		Save(ctx)
	require.NoError(t, err)

	// Test full-text search using raw SQL
	rows, err := client.DB().QueryContext(ctx,
		`SELECT session_id FROM alert_sessions 
		WHERE to_tsvector('english', alert_data) @@ to_tsquery('english', $1)`,
		"error & production",
	)
	require.NoError(t, err)
	defer rows.Close()

	// Collect results
	var results []string
	for rows.Next() {
		var sessionID string
		err := rows.Scan(&sessionID)
		require.NoError(t, err)
		results = append(results, sessionID)
	}

	// Should only match session1
	assert.Len(t, results, 1)
	assert.Equal(t, session1.ID, results[0])

	// Test search for "memory" - should match session2
	rows2, err := client.DB().QueryContext(ctx,
		`SELECT session_id FROM alert_sessions 
		WHERE to_tsvector('english', alert_data) @@ to_tsquery('english', $1)`,
		"memory",
	)
	require.NoError(t, err)
	defer rows2.Close()

	results2 := []string{}
	for rows2.Next() {
		var sessionID string
		err := rows2.Scan(&sessionID)
		require.NoError(t, err)
		results2 = append(results2, sessionID)
	}

	assert.Len(t, results2, 1)
	assert.Equal(t, session2.ID, results2[0])
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: Config{
				Host:         "localhost",
				Port:         5432,
				User:         "test",
				Password:     "test",
				Database:     "test",
				SSLMode:      "disable",
				MaxOpenConns: 10,
				MaxIdleConns: 5,
			},
			wantErr: false,
		},
		{
			name: "missing password",
			cfg: Config{
				Host:         "localhost",
				Port:         5432,
				User:         "test",
				Password:     "",
				Database:     "test",
				MaxOpenConns: 10,
				MaxIdleConns: 5,
			},
			wantErr: true,
		},
		{
			name: "idle conns exceed max conns",
			cfg: Config{
				Host:         "localhost",
				Port:         5432,
				User:         "test",
				Password:     "test",
				Database:     "test",
				MaxOpenConns: 5,
				MaxIdleConns: 10,
			},
			wantErr: true,
		},
		{
			name: "zero max open conns",
			cfg: Config{
				Host:         "localhost",
				Port:         5432,
				User:         "test",
				Password:     "test",
				Database:     "test",
				MaxOpenConns: 0,
				MaxIdleConns: 0,
			},
			wantErr: true,
		},
		{
			name: "negative idle conns",
			cfg: Config{
				Host:         "localhost",
				Port:         5432,
				User:         "test",
				Password:     "test",
				Database:     "test",
				MaxOpenConns: 10,
				MaxIdleConns: -1,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
