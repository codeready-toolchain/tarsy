package database

import (
	"context"
	"database/sql"
	"time"
)

// HealthStatus represents database health and connection pool statistics
type HealthStatus struct {
	Status          string `json:"status"`
	ResponseTime    int64  `json:"response_time_ms"`
	OpenConnections int    `json:"open_connections"`
	InUse           int    `json:"in_use"`
	Idle            int    `json:"idle"`
	WaitCount       int64  `json:"wait_count"`
	WaitDuration    int64  `json:"wait_duration_ms"`
	MaxOpenConns    int    `json:"max_open_conns"`
}

// Health checks database connectivity and returns connection pool statistics
func Health(ctx context.Context, db *sql.DB) (*HealthStatus, error) {
	start := time.Now()

	// Ping database
	if err := db.PingContext(ctx); err != nil {
		return &HealthStatus{
			Status:       "unhealthy",
			ResponseTime: time.Since(start).Milliseconds(),
		}, err
	}

	// Get connection pool stats
	stats := db.Stats()

	return &HealthStatus{
		Status:          "healthy",
		ResponseTime:    time.Since(start).Milliseconds(),
		OpenConnections: stats.OpenConnections,
		InUse:           stats.InUse,
		Idle:            stats.Idle,
		WaitCount:       stats.WaitCount,
		WaitDuration:    stats.WaitDuration.Milliseconds(),
		MaxOpenConns:    stats.MaxOpenConnections,
	}, nil
}
