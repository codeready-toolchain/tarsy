package config

import "time"

// QueueConfig contains queue and worker pool configuration.
// These values control how sessions are polled, claimed, and processed.
type QueueConfig struct {
	// WorkerCount is the number of worker goroutines per replica/pod.
	// Each worker independently polls and processes sessions.
	WorkerCount int `yaml:"worker_count"`

	// MaxConcurrentSessions is the global limit of concurrent sessions being
	// processed across ALL replicas/pods. Enforced by database COUNT(*) check.
	MaxConcurrentSessions int `yaml:"max_concurrent_sessions"`

	// PollInterval is the base interval for checking pending sessions.
	PollInterval time.Duration `yaml:"poll_interval"`

	// PollIntervalJitter is the random jitter added to PollInterval.
	// Actual interval: PollInterval ± PollIntervalJitter.
	PollIntervalJitter time.Duration `yaml:"poll_interval_jitter"`

	// SessionTimeout is the maximum time a session can be processed.
	SessionTimeout time.Duration `yaml:"session_timeout"`

	// GracefulShutdownTimeout is the max time to wait for active sessions
	// to complete during shutdown. Should match SessionTimeout.
	GracefulShutdownTimeout time.Duration `yaml:"graceful_shutdown_timeout"`

	// OrphanDetectionInterval is how often to scan for orphaned sessions.
	OrphanDetectionInterval time.Duration `yaml:"orphan_detection_interval"`

	// OrphanThreshold is how long a session can go without a heartbeat
	// before it is considered orphaned.
	OrphanThreshold time.Duration `yaml:"orphan_threshold"`
}

// DefaultQueueConfig returns the built-in queue defaults.
func DefaultQueueConfig() *QueueConfig {
	return &QueueConfig{
		WorkerCount:             5,
		MaxConcurrentSessions:   5,
		PollInterval:            1 * time.Second,
		PollIntervalJitter:      500 * time.Millisecond,
		SessionTimeout:          15 * time.Minute,
		GracefulShutdownTimeout: 15 * time.Minute,
		OrphanDetectionInterval: 5 * time.Minute,
		OrphanThreshold:         5 * time.Minute,
	}
}
