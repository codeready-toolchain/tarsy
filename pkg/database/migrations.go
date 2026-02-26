package database

import (
	"context"
	"fmt"

	"entgo.io/ent/dialect/sql"
)

// CreateGINIndexes creates full-text search GIN indexes for PostgreSQL.
// These indexes enable efficient full-text search on alert_data and final_analysis fields.
func CreateGINIndexes(ctx context.Context, driver *sql.Driver) error {
	db := driver.DB()

	// GIN index for alert_data full-text search
	_, err := db.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS idx_alert_sessions_alert_data_gin 
		ON alert_sessions USING gin(to_tsvector('english', alert_data))`)
	if err != nil {
		return fmt.Errorf("failed to create alert_data GIN index: %w", err)
	}

	// GIN index for final_analysis full-text search
	_, err = db.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS idx_alert_sessions_final_analysis_gin 
		ON alert_sessions USING gin(to_tsvector('english', COALESCE(final_analysis, '')))`)
	if err != nil {
		return fmt.Errorf("failed to create final_analysis GIN index: %w", err)
	}

	return nil
}

// CreatePartialUniqueIndexes creates PostgreSQL partial unique indexes that
// Ent/Atlas cannot express. These must match the constraints in
// 20260225235224_add_orchestrator_sub_agent_fields.up.sql.
func CreatePartialUniqueIndexes(ctx context.Context, driver *sql.Driver) error {
	db := driver.DB()

	_, err := db.ExecContext(ctx,
		`CREATE UNIQUE INDEX IF NOT EXISTS agentexecution_stage_id_agent_index_top_level
		ON agent_executions (stage_id, agent_index)
		WHERE parent_execution_id IS NULL`)
	if err != nil {
		return fmt.Errorf("failed to create top-level agent index: %w", err)
	}

	_, err = db.ExecContext(ctx,
		`CREATE UNIQUE INDEX IF NOT EXISTS agentexecution_parent_execution_id_agent_index_sub_agent
		ON agent_executions (parent_execution_id, agent_index)
		WHERE parent_execution_id IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("failed to create sub-agent index: %w", err)
	}

	return nil
}
