package database

import (
	"context"
	stdsql "database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// newTestClient creates a test database client with CI/local environment detection.
// In CI (when CI_DATABASE_URL is set): connects to external PostgreSQL service container.
// In local dev: spins up a testcontainer with PostgreSQL.
func newTestClient(t *testing.T) *Client {
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

	// Open database connection using pgx driver
	db, err := stdsql.Open("pgx", connStr)
	require.NoError(t, err)

	// Configure connection pool for tests
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)

	// Create Ent driver from existing database connection
	// Use dialect.Postgres for Ent compatibility while pgx handles the actual connection
	drv := entsql.OpenDB(dialect.Postgres, db)

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

func TestLoadConfigFromEnv(t *testing.T) {
	tests := []struct {
		name        string
		envVars     map[string]string
		wantErr     bool
		errContains string
	}{
		{
			name: "valid config with defaults",
			envVars: map[string]string{
				"DB_PASSWORD": "test",
			},
			wantErr: false,
		},
		{
			name: "valid config with custom values",
			envVars: map[string]string{
				"DB_HOST":            "db.example.com",
				"DB_PORT":            "5433",
				"DB_USER":            "admin",
				"DB_PASSWORD":        "secret",
				"DB_NAME":            "production",
				"DB_SSLMODE":         "require",
				"DB_MAX_OPEN_CONNS":  "50",
				"DB_MAX_IDLE_CONNS":  "20",
			},
			wantErr: false,
		},
		{
			name: "invalid DB_PORT",
			envVars: map[string]string{
				"DB_PORT":     "invalid",
				"DB_PASSWORD": "test",
			},
			wantErr:     true,
			errContains: "invalid DB_PORT",
		},
		{
			name: "invalid DB_MAX_OPEN_CONNS",
			envVars: map[string]string{
				"DB_MAX_OPEN_CONNS": "not_a_number",
				"DB_PASSWORD":       "test",
			},
			wantErr:     true,
			errContains: "invalid DB_MAX_OPEN_CONNS",
		},
		{
			name: "invalid DB_MAX_IDLE_CONNS",
			envVars: map[string]string{
				"DB_MAX_IDLE_CONNS": "abc123",
				"DB_PASSWORD":       "test",
			},
			wantErr:     true,
			errContains: "invalid DB_MAX_IDLE_CONNS",
		},
		{
			name: "invalid DB_CONN_MAX_LIFETIME",
			envVars: map[string]string{
				"DB_CONN_MAX_LIFETIME": "invalid_duration",
				"DB_PASSWORD":          "test",
			},
			wantErr:     true,
			errContains: "invalid DB_CONN_MAX_LIFETIME",
		},
		{
			name: "invalid DB_CONN_MAX_IDLE_TIME",
			envVars: map[string]string{
				"DB_CONN_MAX_IDLE_TIME": "not_a_duration",
				"DB_PASSWORD":           "test",
			},
			wantErr:     true,
			errContains: "invalid DB_CONN_MAX_IDLE_TIME",
		},
		{
			name: "missing password",
			envVars: map[string]string{
				"DB_PASSWORD": "",
			},
			wantErr:     true,
			errContains: "DB_PASSWORD is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear all DB-related env vars
			envKeys := []string{
				"DB_HOST", "DB_PORT", "DB_USER", "DB_PASSWORD", "DB_NAME", "DB_SSLMODE",
				"DB_MAX_OPEN_CONNS", "DB_MAX_IDLE_CONNS",
				"DB_CONN_MAX_LIFETIME", "DB_CONN_MAX_IDLE_TIME",
			}
			for _, key := range envKeys {
				os.Unsetenv(key)
			}

			// Set test env vars
			for key, val := range tt.envVars {
				if val != "" {
					os.Setenv(key, val)
				}
			}

			// Cleanup after test
			t.Cleanup(func() {
				for _, key := range envKeys {
					os.Unsetenv(key)
				}
			})

			// Test LoadConfigFromEnv
			cfg, err := LoadConfigFromEnv()

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				require.NoError(t, err)
				assert.NotNil(t, cfg)
				// Verify defaults are applied
				if tt.name == "valid config with defaults" {
					assert.Equal(t, "localhost", cfg.Host)
					assert.Equal(t, 5432, cfg.Port)
					assert.Equal(t, 25, cfg.MaxOpenConns)
					assert.Equal(t, 10, cfg.MaxIdleConns)
				}
			}
		})
	}
}

func TestHealthStatus_JSONMilliseconds(t *testing.T) {
	client := newTestClient(t)
	ctx := context.Background()

	// Get health status
	health, err := Health(ctx, client.DB())
	require.NoError(t, err)
	require.NotNil(t, health)

	// Verify response time is in milliseconds (can be 0 for very fast local pings)
	assert.GreaterOrEqual(t, health.ResponseTime, int64(0), "response time should be non-negative")
	assert.Less(t, health.ResponseTime, int64(1000), "response time should be less than 1 second for a local ping")

	// Marshal to JSON to verify the output format
	jsonBytes, err := json.Marshal(health)
	require.NoError(t, err)

	// Parse JSON to verify millisecond values
	var jsonData map[string]interface{}
	err = json.Unmarshal(jsonBytes, &jsonData)
	require.NoError(t, err)

	// Verify response_time_ms is a number (not a huge nanosecond value)
	responseTime, ok := jsonData["response_time_ms"].(float64)
	require.True(t, ok, "response_time_ms should be a number")
	assert.GreaterOrEqual(t, responseTime, float64(0), "response_time_ms should be non-negative")
	// If this were nanoseconds, it would be > 1,000,000 (1ms in nanoseconds)
	assert.Less(t, responseTime, float64(1000000), "response_time_ms should be in milliseconds, not nanoseconds")

	// Verify wait_duration_ms is present and is a number
	waitDuration, ok := jsonData["wait_duration_ms"].(float64)
	require.True(t, ok, "wait_duration_ms should be a number")
	assert.GreaterOrEqual(t, waitDuration, float64(0), "wait_duration_ms should be non-negative")
	assert.Less(t, waitDuration, float64(1000000), "wait_duration_ms should be in milliseconds, not nanoseconds")

	t.Logf("Health JSON: %s", string(jsonBytes))
	t.Logf("Response time: %d ms (if this were nanoseconds, it would be > 1,000,000)", health.ResponseTime)
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
