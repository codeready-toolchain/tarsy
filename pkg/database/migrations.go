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
