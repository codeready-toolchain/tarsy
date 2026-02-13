package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// EventPublisher publishes events for WebSocket delivery.
// Persistent events are stored in the events table then broadcast via NOTIFY.
// Transient events (streaming chunks) are broadcast via NOTIFY only.
//
// Each public method accepts a specific typed payload struct — see payloads.go.
// Internally, payloads are marshaled to JSON and routed to the appropriate
// channel (derived from sessionID) via persistAndNotify or notifyOnly.
type EventPublisher struct {
	db *sql.DB
}

// NewEventPublisher creates a new EventPublisher.
// The db parameter should be the *sql.DB from database.Client.DB().
func NewEventPublisher(db *sql.DB) *EventPublisher {
	return &EventPublisher{db: db}
}

// --- Typed public methods ---

// PublishTimelineCreated persists and broadcasts a timeline_event.created event.
// Used when a new timeline event is created (streaming or completed).
func (p *EventPublisher) PublishTimelineCreated(ctx context.Context, sessionID string, payload TimelineCreatedPayload) error {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal TimelineCreatedPayload: %w", err)
	}
	return p.persistAndNotify(ctx, sessionID, SessionChannel(sessionID), payloadJSON)
}

// PublishTimelineCompleted persists and broadcasts a timeline_event.completed event.
// Used when a streaming timeline event transitions to a terminal status.
func (p *EventPublisher) PublishTimelineCompleted(ctx context.Context, sessionID string, payload TimelineCompletedPayload) error {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal TimelineCompletedPayload: %w", err)
	}
	return p.persistAndNotify(ctx, sessionID, SessionChannel(sessionID), payloadJSON)
}

// PublishStreamChunk broadcasts a stream.chunk transient event (no DB persistence).
// Used for high-frequency LLM streaming tokens — ephemeral, lost on disconnect.
func (p *EventPublisher) PublishStreamChunk(ctx context.Context, sessionID string, payload StreamChunkPayload) error {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal StreamChunkPayload: %w", err)
	}
	return p.notifyOnly(ctx, SessionChannel(sessionID), payloadJSON)
}

// PublishStageStatus persists and broadcasts a stage.status event.
// Used for stage lifecycle transitions (started, completed, failed, etc.).
func (p *EventPublisher) PublishStageStatus(ctx context.Context, sessionID string, payload StageStatusPayload) error {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal StageStatusPayload: %w", err)
	}
	return p.persistAndNotify(ctx, sessionID, SessionChannel(sessionID), payloadJSON)
}

// PublishSessionStatus persists a session status event to the session channel
// and broadcasts a transient copy to the global sessions channel.
// Both publishes are best-effort: if the persistent one fails, the transient
// one is still attempted. Returns the first error encountered (if any).
func (p *EventPublisher) PublishSessionStatus(ctx context.Context, sessionID string, payload SessionStatusPayload) error {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal SessionStatusPayload: %w", err)
	}

	// Persist to session-specific channel
	var firstErr error
	if err := p.persistAndNotify(ctx, sessionID, SessionChannel(sessionID), payloadJSON); err != nil {
		slog.Warn("Failed to publish session status to session channel",
			"session_id", sessionID, "status", payload.Status, "error", err)
		firstErr = err
	}

	// Also broadcast to global sessions channel (transient — for session list page)
	if err := p.notifyOnly(ctx, GlobalSessionsChannel, payloadJSON); err != nil {
		slog.Warn("Failed to publish session status to global channel",
			"session_id", sessionID, "status", payload.Status, "error", err)
		if firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

// PublishChatCreated persists and broadcasts a chat.created event.
// Used when a new chat is created for a session (first message).
func (p *EventPublisher) PublishChatCreated(ctx context.Context, sessionID string, payload ChatCreatedPayload) error {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal ChatCreatedPayload: %w", err)
	}
	return p.persistAndNotify(ctx, sessionID, SessionChannel(sessionID), payloadJSON)
}

// PublishInteractionCreated persists and broadcasts an interaction.created event.
// Fired when an LLM or MCP interaction record is saved to the database.
func (p *EventPublisher) PublishInteractionCreated(ctx context.Context, sessionID string, payload InteractionCreatedPayload) error {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal InteractionCreatedPayload: %w", err)
	}
	return p.persistAndNotify(ctx, sessionID, SessionChannel(sessionID), payloadJSON)
}

// PublishSessionProgress broadcasts a session.progress transient event (no DB persistence).
// Published to the global sessions channel for the active alerts panel.
func (p *EventPublisher) PublishSessionProgress(ctx context.Context, payload SessionProgressPayload) error {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal SessionProgressPayload: %w", err)
	}
	return p.notifyOnly(ctx, GlobalSessionsChannel, payloadJSON)
}

// PublishExecutionProgress broadcasts an execution.progress transient event (no DB persistence).
// Published to the session channel for per-agent progress display.
func (p *EventPublisher) PublishExecutionProgress(ctx context.Context, sessionID string, payload ExecutionProgressPayload) error {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal ExecutionProgressPayload: %w", err)
	}
	return p.notifyOnly(ctx, SessionChannel(sessionID), payloadJSON)
}

// --- Internal core methods ---

// persistAndNotify persists a pre-marshaled event to the database and broadcasts
// via NOTIFY in a single transaction (pg_notify is transactional — held until COMMIT).
func (p *EventPublisher) persistAndNotify(ctx context.Context, sessionID, channel string, payloadJSON []byte) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// 1. Persist to events table (within transaction)
	var eventID int64
	err = tx.QueryRowContext(ctx,
		`INSERT INTO events (session_id, channel, payload, created_at) VALUES ($1, $2, $3, $4) RETURNING id`,
		sessionID, channel, payloadJSON, time.Now(),
	).Scan(&eventID)
	if err != nil {
		return fmt.Errorf("failed to persist event: %w", err)
	}

	// Build NOTIFY payload with db_event_id for catchup tracking.
	notifyPayload, err := injectDBEventIDAndTruncate(payloadJSON, eventID)
	if err != nil {
		return err
	}

	// 2. pg_notify within same transaction — held until COMMIT
	_, err = tx.ExecContext(ctx, "SELECT pg_notify($1, $2)", channel, notifyPayload)
	if err != nil {
		return fmt.Errorf("pg_notify failed: %w", err)
	}

	// 3. Commit — INSERT is persisted and NOTIFY fires atomically
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit event transaction: %w", err)
	}

	return nil
}

// notifyOnly broadcasts a pre-marshaled event via NOTIFY without persisting to DB.
func (p *EventPublisher) notifyOnly(ctx context.Context, channel string, payloadJSON []byte) error {
	notifyPayload, err := truncateIfNeeded(string(payloadJSON))
	if err != nil {
		return err
	}
	_, err = p.db.ExecContext(ctx, "SELECT pg_notify($1, $2)", channel, notifyPayload)
	if err != nil {
		return fmt.Errorf("pg_notify failed: %w", err)
	}
	return nil
}

// --- Internal helpers ---

// injectDBEventIDAndTruncate adds db_event_id to the JSON payload for NOTIFY
// delivery and applies truncation if the result exceeds PostgreSQL's limit.
func injectDBEventIDAndTruncate(payloadJSON []byte, dbEventID int64) (string, error) {
	var m map[string]any
	if err := json.Unmarshal(payloadJSON, &m); err != nil {
		return "", fmt.Errorf("failed to unmarshal payload for db_event_id injection: %w", err)
	}
	m["db_event_id"] = dbEventID

	enrichedBytes, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("failed to marshal enriched NOTIFY payload: %w", err)
	}

	return truncateIfNeeded(string(enrichedBytes))
}

// truncateIfNeeded returns the payload string as-is if it fits within
// PostgreSQL's 8000-byte NOTIFY limit, otherwise returns a minimal
// truncation envelope with only routing fields.
func truncateIfNeeded(payloadStr string) (string, error) {
	if len(payloadStr) <= 7900 {
		return payloadStr, nil
	}
	return buildTruncatedPayload([]byte(payloadStr))
}

// buildTruncatedPayload creates a minimal truncation envelope from the full
// JSON payload bytes, extracting only the routing fields the client needs
// to fetch the complete event from the database.
func buildTruncatedPayload(payloadBytes []byte) (string, error) {
	var routing struct {
		Type      string `json:"type"`
		EventID   string `json:"event_id"`
		SessionID string `json:"session_id"`
		DBEventID *int64 `json:"db_event_id,omitempty"`
	}
	if err := json.Unmarshal(payloadBytes, &routing); err != nil {
		return "", fmt.Errorf("failed to extract routing fields for truncation: %w", err)
	}

	truncated := map[string]any{
		"type":       routing.Type,
		"event_id":   routing.EventID,
		"session_id": routing.SessionID,
		"truncated":  true,
	}
	if routing.DBEventID != nil {
		truncated["db_event_id"] = *routing.DBEventID
	}

	truncBytes, err := json.Marshal(truncated)
	if err != nil {
		return "", fmt.Errorf("failed to marshal truncated payload: %w", err)
	}
	return string(truncBytes), nil
}
