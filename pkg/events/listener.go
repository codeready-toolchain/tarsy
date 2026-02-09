package events

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
)

// listenCmd represents a LISTEN/UNLISTEN command to be executed by the
// receive loop, which is the sole goroutine that touches the pgx connection.
type listenCmd struct {
	sql    string
	result chan error
}

// NotifyListener listens for PostgreSQL NOTIFY events and dispatches
// them to the local ConnectionManager.
type NotifyListener struct {
	connString string
	conn       *pgx.Conn // Dedicated connection for LISTEN
	connMu     sync.Mutex
	manager    *ConnectionManager
	channels   map[string]bool // Currently LISTENing channels
	channelsMu sync.RWMutex

	// cmdCh serializes LISTEN/UNLISTEN through the receive loop, which is the
	// sole user of the pgx connection. This avoids the "conn busy" race between
	// WaitForNotification and Exec.
	cmdCh   chan listenCmd
	running atomic.Bool

	// cancelLoop and loopDone coordinate graceful shutdown of the receive loop.
	cancelLoop context.CancelFunc
	loopDone   chan struct{}
}

// NewNotifyListener creates a new PostgreSQL NOTIFY listener.
func NewNotifyListener(connString string, manager *ConnectionManager) *NotifyListener {
	return &NotifyListener{
		connString: connString,
		manager:    manager,
		channels:   make(map[string]bool),
		cmdCh:      make(chan listenCmd, 16),
	}
}

// Start establishes the dedicated LISTEN connection and begins receiving notifications.
func (l *NotifyListener) Start(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, l.connString)
	if err != nil {
		return fmt.Errorf("failed to connect for LISTEN: %w", err)
	}

	l.connMu.Lock()
	l.conn = conn
	l.connMu.Unlock()

	l.running.Store(true)

	// Start the notification receive loop with a cancellable context
	// so Stop() can signal it to exit before closing the connection.
	loopCtx, cancel := context.WithCancel(ctx)
	l.cancelLoop = cancel
	l.loopDone = make(chan struct{})
	go func() {
		defer close(l.loopDone)
		l.receiveLoop(loopCtx)
	}()

	slog.Info("NotifyListener started")
	return nil
}

// Subscribe sends LISTEN for a channel on the dedicated connection.
// The command is executed by the receive loop to avoid concurrent pgx access.
func (l *NotifyListener) Subscribe(ctx context.Context, channel string) error {
	l.channelsMu.Lock()
	if l.channels[channel] {
		l.channelsMu.Unlock()
		return nil // Already listening
	}
	l.channelsMu.Unlock()

	if !l.running.Load() {
		return fmt.Errorf("LISTEN connection not established")
	}

	sanitized := pgx.Identifier{channel}.Sanitize()
	cmd := listenCmd{
		sql:    "LISTEN " + sanitized,
		result: make(chan error, 1),
	}

	select {
	case l.cmdCh <- cmd:
	case <-ctx.Done():
		return ctx.Err()
	}

	select {
	case err := <-cmd.result:
		if err != nil {
			return fmt.Errorf("LISTEN %s failed: %w", sanitized, err)
		}
		l.channelsMu.Lock()
		l.channels[channel] = true
		l.channelsMu.Unlock()
		slog.Debug("Subscribed to NOTIFY channel", "channel", channel)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Unsubscribe sends UNLISTEN for a channel.
func (l *NotifyListener) Unsubscribe(ctx context.Context, channel string) error {
	l.channelsMu.Lock()
	if !l.channels[channel] {
		l.channelsMu.Unlock()
		return nil // Not listening
	}
	l.channelsMu.Unlock()

	if !l.running.Load() {
		return nil
	}

	sanitized := pgx.Identifier{channel}.Sanitize()
	cmd := listenCmd{
		sql:    "UNLISTEN " + sanitized,
		result: make(chan error, 1),
	}

	select {
	case l.cmdCh <- cmd:
	case <-ctx.Done():
		return ctx.Err()
	}

	select {
	case err := <-cmd.result:
		if err != nil {
			return fmt.Errorf("UNLISTEN %s failed: %w", sanitized, err)
		}
		l.channelsMu.Lock()
		delete(l.channels, channel)
		l.channelsMu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// receiveLoop continuously receives notifications from PostgreSQL
// and dispatches them to the ConnectionManager.
// It is the sole goroutine that touches the pgx connection, avoiding
// concurrent access races between WaitForNotification and Exec.
func (l *NotifyListener) receiveLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Process any pending LISTEN/UNLISTEN commands first
		l.processPendingCmds(ctx)

		l.connMu.Lock()
		conn := l.conn
		l.connMu.Unlock()

		if conn == nil {
			// Connection lost, try to reconnect
			l.reconnect(ctx)
			continue
		}

		// Use a short timeout so we periodically return to process
		// pending LISTEN/UNLISTEN commands from the cmdCh.
		waitCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		notification, err := conn.WaitForNotification(waitCtx)
		cancel()

		if err != nil {
			if ctx.Err() != nil {
				return // Context cancelled — shutting down
			}
			if waitCtx.Err() != nil {
				continue // Timeout — loop back to check commands
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

// processPendingCmds drains the command channel and executes each
// LISTEN/UNLISTEN SQL command on the pgx connection.
func (l *NotifyListener) processPendingCmds(ctx context.Context) {
	for {
		select {
		case cmd := <-l.cmdCh:
			l.connMu.Lock()
			conn := l.conn
			l.connMu.Unlock()

			if conn == nil {
				cmd.result <- fmt.Errorf("LISTEN connection not established")
				continue
			}

			_, err := conn.Exec(ctx, cmd.sql)
			cmd.result <- err
		default:
			return
		}
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
			sanitized := pgx.Identifier{ch}.Sanitize()
			if _, err := conn.Exec(ctx, "LISTEN "+sanitized); err != nil {
				slog.Error("Re-LISTEN failed", "channel", ch, "error", err)
			}
		}
		l.channelsMu.RUnlock()

		slog.Info("NotifyListener reconnected")
		return
	}
}

// Stop signals the receive loop to exit, waits for it to finish,
// then closes the LISTEN connection.
func (l *NotifyListener) Stop(ctx context.Context) {
	l.running.Store(false)

	// Signal the receive loop to exit and wait for it to finish
	// before closing the connection. This prevents a race between
	// WaitForNotification and conn.Close().
	if l.cancelLoop != nil {
		l.cancelLoop()
	}
	if l.loopDone != nil {
		<-l.loopDone
	}

	l.connMu.Lock()
	defer l.connMu.Unlock()
	if l.conn != nil {
		_ = l.conn.Close(ctx)
		l.conn = nil
	}
}
