// Package cleanup provides data retention and cleanup services.
package cleanup

import (
	"context"
	"log/slog"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/services"
)

// Service periodically enforces retention policies:
//   - Soft-deletes old sessions (completed + stale pending)
//   - Removes orphaned Event rows past their TTL
//
// All operations are idempotent and safe to run from multiple pods.
type Service struct {
	config         *config.RetentionConfig
	sessionService *services.SessionService
	eventService   *services.EventService

	cancel context.CancelFunc
	done   chan struct{}
}

// NewService creates a new cleanup service.
func NewService(
	cfg *config.RetentionConfig,
	sessionService *services.SessionService,
	eventService *services.EventService,
) *Service {
	return &Service{
		config:         cfg,
		sessionService: sessionService,
		eventService:   eventService,
	}
}

// Start launches the background cleanup loop.
func (s *Service) Start(ctx context.Context) {
	if s.cancel != nil {
		return
	}
	ctx, s.cancel = context.WithCancel(ctx)
	s.done = make(chan struct{})

	go s.run(ctx)

	slog.Info("Cleanup service started",
		"session_retention_days", s.config.SessionRetentionDays,
		"event_ttl", s.config.EventTTL,
		"interval", s.config.CleanupInterval)
}

// Stop signals the cleanup loop to exit and waits for it to finish.
func (s *Service) Stop() {
	if s.cancel == nil {
		return
	}
	s.cancel()
	<-s.done
	slog.Info("Cleanup service stopped")
}

func (s *Service) run(ctx context.Context) {
	defer close(s.done)

	s.runAll(ctx)

	ticker := time.NewTicker(s.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runAll(ctx)
		}
	}
}

func (s *Service) runAll(ctx context.Context) {
	s.softDeleteOldSessions(ctx)
	s.cleanupOrphanedEvents(ctx)
}

func (s *Service) softDeleteOldSessions(_ context.Context) {
	count, err := s.sessionService.SoftDeleteOldSessions(context.Background(), s.config.SessionRetentionDays)
	if err != nil {
		slog.Error("Retention: soft-delete sessions failed", "error", err)
		return
	}
	if count > 0 {
		slog.Info("Retention: soft-deleted old sessions", "count", count)
	}
}

func (s *Service) cleanupOrphanedEvents(_ context.Context) {
	count, err := s.eventService.CleanupOrphanedEvents(context.Background(), s.config.EventTTL)
	if err != nil {
		slog.Error("Retention: event cleanup failed", "error", err)
		return
	}
	if count > 0 {
		slog.Info("Retention: cleaned up orphaned events", "count", count)
	}
}
