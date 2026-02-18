package slack

import (
	"context"
	"log/slog"
	"time"
)

// ServiceConfig holds the parameters needed to construct a Service.
type ServiceConfig struct {
	Token        string
	Channel      string
	DashboardURL string
}

// SessionStartedInput contains data for a session start notification.
type SessionStartedInput struct {
	SessionID               string
	AlertType               string
	SlackMessageFingerprint string
}

// SessionCompletedInput contains data for a terminal session notification.
type SessionCompletedInput struct {
	SessionID               string
	AlertType               string
	Status                  string // completed, failed, timed_out, cancelled
	ExecutiveSummary        string
	FinalAnalysis           string
	ErrorMessage            string
	SlackMessageFingerprint string
	ThreadTS                string // Cached from start notification
}

// Service handles Slack notification delivery.
// Nil-safe: all methods are no-ops when service is nil.
type Service struct {
	client       *Client
	dashboardURL string
	logger       *slog.Logger
}

// NewService creates a new Slack notification service.
// Returns nil if Token or Channel is empty.
func NewService(cfg ServiceConfig) *Service {
	if cfg.Token == "" || cfg.Channel == "" {
		return nil
	}
	return &Service{
		client:       NewClient(cfg.Token, cfg.Channel),
		dashboardURL: cfg.DashboardURL,
		logger:       slog.Default().With("component", "slack-service"),
	}
}

// NewServiceWithClient creates a Service backed by a pre-built Client.
// Useful for testing with a mock API server.
func NewServiceWithClient(client *Client, dashboardURL string) *Service {
	return &Service{
		client:       client,
		dashboardURL: dashboardURL,
		logger:       slog.Default().With("component", "slack-service"),
	}
}

// NotifySessionStarted sends a "processing started" notification.
// Only sends if fingerprint is present (Slack-originated alerts).
// Returns resolved threadTS for reuse by terminal notification.
// Fail-open: errors are logged, never returned.
func (s *Service) NotifySessionStarted(ctx context.Context, input SessionStartedInput) string {
	if s == nil {
		return ""
	}

	if input.SlackMessageFingerprint == "" {
		return ""
	}

	threadTS, err := s.client.FindMessageByFingerprint(ctx, input.SlackMessageFingerprint)
	if err != nil {
		s.logger.Warn("Failed to find Slack thread for fingerprint",
			"session_id", input.SessionID,
			"fingerprint", input.SlackMessageFingerprint,
			"error", err)
	}

	blocks := BuildStartedMessage(input.SessionID, s.dashboardURL)
	if err := s.client.PostMessage(ctx, blocks, threadTS, 5*time.Second); err != nil {
		s.logger.Error("Failed to send Slack start notification",
			"session_id", input.SessionID,
			"error", err)
	}

	return threadTS
}

// NotifySessionCompleted sends a terminal status notification.
// Fail-open: errors are logged, never returned.
func (s *Service) NotifySessionCompleted(ctx context.Context, input SessionCompletedInput) {
	if s == nil {
		return
	}

	threadTS := input.ThreadTS
	if threadTS == "" && input.SlackMessageFingerprint != "" {
		var err error
		threadTS, err = s.client.FindMessageByFingerprint(ctx, input.SlackMessageFingerprint)
		if err != nil {
			s.logger.Warn("Failed to find Slack thread for fingerprint",
				"session_id", input.SessionID,
				"fingerprint", input.SlackMessageFingerprint,
				"error", err)
		}
	}

	blocks := BuildTerminalMessage(input, s.dashboardURL)
	if err := s.client.PostMessage(ctx, blocks, threadTS, 10*time.Second); err != nil {
		s.logger.Error("Failed to send Slack notification",
			"session_id", input.SessionID,
			"status", input.Status,
			"error", err)
	}
}
