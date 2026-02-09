package events

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// NotifyListener listens for PostgreSQL NOTIFY events and dispatches
// them to the local ConnectionManager.
type NotifyListener struct {
	connString string
	conn       *pgx.Conn // Dedicated connection for LISTEN
	connMu     sync.Mutex
	manager    *ConnectionManager
	channels   map[string]bool // Currently LISTENing channels
	channelsMu sync.RWMutex
}

// NewNotifyListener creates a new PostgreSQL NOTIFY listener.
func NewNotifyListener(connString string, manager *ConnectionManager) *NotifyListener {
	return &NotifyListener{
		connString: connString,
		manager:    manager,
		channels:   make(map[string]bool),
	}
}

// Start establishes the dedicated LISTEN connection and begins receiving notifications.
func (l *NotifyListener) Start(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, l.connString)
	if err != nil {
		return fmt.Errorf("failed to connect for LISTEN: %w", err)
	}
	l.conn = conn

	// Start the notification receive loop
	go l.receiveLoop(ctx)

	slog.Info("NotifyListener started")
	return nil
}

// Subscribe sends LISTEN for a channel on the dedicated connection.
func (l *NotifyListener) Subscribe(ctx context.Context, channel string) error {
	l.channelsMu.Lock()
	defer l.channelsMu.Unlock()

	if l.channels[channel] {
		return nil // Already listening
	}

	l.connMu.Lock()
	defer l.connMu.Unlock()

	if l.conn == nil {
		return fmt.Errorf("LISTEN connection not established")
	}

	// LISTEN requires a direct SQL command (not parameterized).
	// Use quoted identifier to prevent SQL injection.
	_, err := l.conn.Exec(ctx, fmt.Sprintf("LISTEN %q", channel))
	if err != nil {
		return fmt.Errorf("LISTEN %q failed: %w", channel, err)
	}

	l.channels[channel] = true
	slog.Debug("Subscribed to NOTIFY channel", "channel", channel)
	return nil
}

// Unsubscribe sends UNLISTEN for a channel.
func (l *NotifyListener) Unsubscribe(ctx context.Context, channel string) error {
	l.channelsMu.Lock()
	defer l.channelsMu.Unlock()

	if !l.channels[channel] {
		return nil // Not listening
	}

	l.connMu.Lock()
	defer l.connMu.Unlock()

	if l.conn == nil {
		return nil
	}

	_, err := l.conn.Exec(ctx, fmt.Sprintf("UNLISTEN %q", channel))
	if err != nil {
		return fmt.Errorf("UNLISTEN %q failed: %w", channel, err)
	}

	delete(l.channels, channel)
	return nil
}

// receiveLoop continuously receives notifications from PostgreSQL
// and dispatches them to the ConnectionManager.
func (l *NotifyListener) receiveLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		l.connMu.Lock()
		conn := l.conn
		l.connMu.Unlock()

		if conn == nil {
			// Connection lost, try to reconnect
			l.reconnect(ctx)
			continue
		}

		notification, err := conn.WaitForNotification(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return // Context cancelled — shutting down
			}
			slog.Error("NOTIFY receive error", "error", err)
			// Attempt reconnection
			l.reconnect(ctx)
			continue
		}

		// Dispatch to ConnectionManager
		l.manager.Broadcast(notification.Channel, []byte(notification.Payload))
	}
}

// reconnect attempts to re-establish the LISTEN connection.
func (l *NotifyListener) reconnect(ctx context.Context) {
	l.connMu.Lock()
	defer l.connMu.Unlock()

	// Close old connection
	if l.conn != nil {
		_ = l.conn.Close(ctx)
		l.conn = nil
	}

	// Exponential backoff reconnection
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		conn, err := pgx.Connect(ctx, l.connString)
		if err != nil {
			slog.Error("LISTEN reconnect failed", "error", err, "backoff", backoff)
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		l.conn = conn

		// Re-subscribe to all channels
		l.channelsMu.RLock()
		for ch := range l.channels {
			if _, err := conn.Exec(ctx, fmt.Sprintf("LISTEN %q", ch)); err != nil {
				slog.Error("Re-LISTEN failed", "channel", ch, "error", err)
			}
		}
		l.channelsMu.RUnlock()

		slog.Info("NotifyListener reconnected")
		return
	}
}

// Stop closes the LISTEN connection.
func (l *NotifyListener) Stop(ctx context.Context) {
	l.connMu.Lock()
	defer l.connMu.Unlock()
	if l.conn != nil {
		_ = l.conn.Close(ctx)
		l.conn = nil
	}
}
