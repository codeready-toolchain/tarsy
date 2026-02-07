package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultQueueConfig(t *testing.T) {
	cfg := DefaultQueueConfig()

	assert.Equal(t, 5, cfg.WorkerCount)
	assert.Equal(t, 5, cfg.MaxConcurrentSessions)
	assert.Equal(t, 1*time.Second, cfg.PollInterval)
	assert.Equal(t, 500*time.Millisecond, cfg.PollIntervalJitter)
	assert.Equal(t, 15*time.Minute, cfg.SessionTimeout)
	assert.Equal(t, 15*time.Minute, cfg.GracefulShutdownTimeout)
	assert.Equal(t, 5*time.Minute, cfg.OrphanDetectionInterval)
	assert.Equal(t, 5*time.Minute, cfg.OrphanThreshold)
	assert.Equal(t, 30*time.Second, cfg.HeartbeatInterval)
}

func TestValidateQueue(t *testing.T) {
	tests := []struct {
		name    string
		queue   *QueueConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid defaults",
			queue:   DefaultQueueConfig(),
			wantErr: false,
		},
		{
			name:    "nil queue",
			queue:   nil,
			wantErr: true,
			errMsg:  "queue configuration is nil",
		},
		{
			name: "worker count too low",
			queue: func() *QueueConfig {
				q := DefaultQueueConfig()
				q.WorkerCount = 0
				return q
			}(),
			wantErr: true,
			errMsg:  "worker_count must be between 1 and 50",
		},
		{
			name: "worker count too high",
			queue: func() *QueueConfig {
				q := DefaultQueueConfig()
				q.WorkerCount = 51
				return q
			}(),
			wantErr: true,
			errMsg:  "worker_count must be between 1 and 50",
		},
		{
			name: "max concurrent sessions zero",
			queue: func() *QueueConfig {
				q := DefaultQueueConfig()
				q.MaxConcurrentSessions = 0
				return q
			}(),
			wantErr: true,
			errMsg:  "max_concurrent_sessions must be at least 1",
		},
		{
			name: "poll interval zero",
			queue: func() *QueueConfig {
				q := DefaultQueueConfig()
				q.PollInterval = 0
				return q
			}(),
			wantErr: true,
			errMsg:  "poll_interval must be positive",
		},
		{
			name: "negative jitter",
			queue: func() *QueueConfig {
				q := DefaultQueueConfig()
				q.PollIntervalJitter = -1 * time.Second
				return q
			}(),
			wantErr: true,
			errMsg:  "poll_interval_jitter must be non-negative",
		},
		{
			name: "session timeout zero",
			queue: func() *QueueConfig {
				q := DefaultQueueConfig()
				q.SessionTimeout = 0
				return q
			}(),
			wantErr: true,
			errMsg:  "session_timeout must be positive",
		},
		{
			name: "graceful shutdown timeout zero",
			queue: func() *QueueConfig {
				q := DefaultQueueConfig()
				q.GracefulShutdownTimeout = 0
				return q
			}(),
			wantErr: true,
			errMsg:  "graceful_shutdown_timeout must be positive",
		},
		{
			name: "orphan detection interval zero",
			queue: func() *QueueConfig {
				q := DefaultQueueConfig()
				q.OrphanDetectionInterval = 0
				return q
			}(),
			wantErr: true,
			errMsg:  "orphan_detection_interval must be positive",
		},
		{
			name: "orphan threshold zero",
			queue: func() *QueueConfig {
				q := DefaultQueueConfig()
				q.OrphanThreshold = 0
				return q
			}(),
			wantErr: true,
			errMsg:  "orphan_threshold must be positive",
		},
		{
			name: "zero jitter is valid",
			queue: func() *QueueConfig {
				q := DefaultQueueConfig()
				q.PollIntervalJitter = 0
				return q
			}(),
			wantErr: false,
		},
		{
			name: "jitter equal to poll interval",
			queue: func() *QueueConfig {
				q := DefaultQueueConfig()
				q.PollInterval = 1 * time.Second
				q.PollIntervalJitter = 1 * time.Second
				return q
			}(),
			wantErr: true,
			errMsg:  "poll_interval_jitter must be less than poll_interval",
		},
		{
			name: "jitter greater than poll interval",
			queue: func() *QueueConfig {
				q := DefaultQueueConfig()
				q.PollInterval = 500 * time.Millisecond
				q.PollIntervalJitter = 1 * time.Second
				return q
			}(),
			wantErr: true,
			errMsg:  "poll_interval_jitter must be less than poll_interval",
		},
		{
			name: "jitter slightly less than poll interval is valid",
			queue: func() *QueueConfig {
				q := DefaultQueueConfig()
				q.PollInterval = 1 * time.Second
				q.PollIntervalJitter = 999 * time.Millisecond
				return q
			}(),
			wantErr: false,
		},
		{
			name: "heartbeat interval zero",
			queue: func() *QueueConfig {
				q := DefaultQueueConfig()
				q.HeartbeatInterval = 0
				return q
			}(),
			wantErr: true,
			errMsg:  "heartbeat_interval must be positive",
		},
		{
			name: "heartbeat interval equal to orphan threshold",
			queue: func() *QueueConfig {
				q := DefaultQueueConfig()
				q.OrphanThreshold = 1 * time.Minute
				q.HeartbeatInterval = 1 * time.Minute
				return q
			}(),
			wantErr: true,
			errMsg:  "heartbeat_interval must be less than orphan_threshold",
		},
		{
			name: "heartbeat interval greater than orphan threshold",
			queue: func() *QueueConfig {
				q := DefaultQueueConfig()
				q.OrphanThreshold = 1 * time.Minute
				q.HeartbeatInterval = 2 * time.Minute
				return q
			}(),
			wantErr: true,
			errMsg:  "heartbeat_interval must be less than orphan_threshold",
		},
		{
			name: "heartbeat interval slightly less than orphan threshold is valid",
			queue: func() *QueueConfig {
				q := DefaultQueueConfig()
				q.OrphanThreshold = 5 * time.Minute
				q.HeartbeatInterval = 30 * time.Second
				return q
			}(),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{Queue: tt.queue}
			v := NewValidator(cfg)
			err := v.validateQueue()

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
