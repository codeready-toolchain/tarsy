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
type EventPublisher struct {
	db *sql.DB
}

// NewEventPublisher creates a new EventPublisher.
// The db parameter should be the *sql.DB from database.Client.DB().
func NewEventPublisher(db *sql.DB) *EventPublisher {
	return &EventPublisher{db: db}
}

// Publish persists an event to the database and broadcasts via NOTIFY
// in a single transaction (pg_notify is transactional — held until COMMIT).
// Used for persistent events (timeline_event.created, timeline_event.completed,
// session.status, etc.) that must survive reconnection.
func (p *EventPublisher) Publish(ctx context.Context, sessionID, channel string, payload map[string]interface{}) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal event payload: %w", err)
	}

	// Single transaction: INSERT event + pg_notify().
	// pg_notify() is transactional — notification is held until COMMIT,
	// so WebSocket clients never receive events for uncommitted data.
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// 1. Persist to events table (within transaction)
	var eventID int64
	err = tx.QueryRowContext(ctx,
		`INSERT INTO events (session_id, channel, payload, created_at) VALUES ($1, $2, $3, $4) RETURNING id`,
		sessionID, channel, payloadBytes, time.Now(),
	).Scan(&eventID)
	if err != nil {
		return fmt.Errorf("failed to persist event: %w", err)
	}

	// Include the DB event ID in the payload for catchup tracking
	payload["db_event_id"] = eventID
	notifyPayload, err := marshalPayload(payload)
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

// PublishTransient broadcasts an event via NOTIFY without persisting to DB.
// Used for high-frequency events like LLM streaming token chunks where
// per-event DB writes would cause write amplification.
// These events are ephemeral — lost if the client is disconnected.
func (p *EventPublisher) PublishTransient(ctx context.Context, channel string, payload map[string]interface{}) error {
	notifyPayload, err := marshalPayload(payload)
	if err != nil {
		return err
	}
	// No transaction needed — transient events are fire-and-forget
	_, err = p.db.ExecContext(ctx, "SELECT pg_notify($1, $2)", channel, notifyPayload)
	if err != nil {
		return fmt.Errorf("pg_notify failed: %w", err)
	}
	return nil
}

// PublishNonBlocking is a convenience wrapper that logs errors instead of
// returning them. Used by controllers where event publishing must not
// interrupt agent execution.
func (p *EventPublisher) PublishNonBlocking(ctx context.Context, sessionID, channel string, payload map[string]interface{}) {
	if err := p.Publish(ctx, sessionID, channel, payload); err != nil {
		slog.Warn("Failed to publish event",
			"session_id", sessionID,
			"channel", channel,
			"type", payload["type"],
			"error", err)
	}
}

// PublishTransientNonBlocking is a convenience wrapper that logs errors
// instead of returning them.
func (p *EventPublisher) PublishTransientNonBlocking(ctx context.Context, channel string, payload map[string]interface{}) {
	if err := p.PublishTransient(ctx, channel, payload); err != nil {
		slog.Warn("Failed to publish transient event",
			"channel", channel,
			"type", payload["type"],
			"error", err)
	}
}

// marshalPayload serializes the payload to JSON, truncating if it exceeds
// PostgreSQL's 8000-byte NOTIFY payload limit.
func marshalPayload(payload map[string]interface{}) (string, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal NOTIFY payload: %w", err)
	}

	payloadStr := string(payloadBytes)

	// PostgreSQL NOTIFY payload limit is 8000 bytes.
	// If payload exceeds limit, send a truncated notification with a flag
	// indicating the client should fetch the full event from DB.
	if len(payloadStr) > 7900 {
		truncated := map[string]interface{}{
			"type":       payload["type"],
			"event_id":   payload["event_id"],
			"session_id": payload["session_id"],
			"truncated":  true,
		}
		payloadBytes, _ = json.Marshal(truncated)
		payloadStr = string(payloadBytes)
	}

	return payloadStr, nil
}
