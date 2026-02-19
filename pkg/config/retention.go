package config

import "time"

// RetentionConfig controls data retention and cleanup behavior.
type RetentionConfig struct {
	// SessionRetentionDays is how many days to keep completed sessions
	// before soft-deleting them (setting deleted_at).
	SessionRetentionDays int `yaml:"session_retention_days"`

	// EventTTL is the maximum age of orphaned Event rows before deletion.
	// Per-session cleanup handles the normal case; this is a safety net.
	EventTTL time.Duration `yaml:"event_ttl"`

	// CleanupInterval is how often the cleanup loop runs.
	CleanupInterval time.Duration `yaml:"cleanup_interval"`
}

// DefaultRetentionConfig returns the built-in retention defaults.
func DefaultRetentionConfig() *RetentionConfig {
	return &RetentionConfig{
		SessionRetentionDays: 365,
		EventTTL:             1 * time.Hour,
		CleanupInterval:      12 * time.Hour,
	}
}
