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
	sql     string
	channel string // channel name (used for generation checks on UNLISTEN)
	gen     uint64 // generation at Unsubscribe time; 0 for LISTEN (always execute)
	result  chan error
}

// NotifyListener listens for PostgreSQL NOTIFY events and dispatches
// them to the local ConnectionManager (for WebSocket clients) and to
// registered internal handlers (for backend-to-backend communication).
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

	// listenGen tracks per-channel generation counters to prevent stale
	// UNLISTENs from winning a race against a newer LISTEN. The generation is
	// incremented by the receive loop (processPendingCmds) when a LISTEN
	// command is successfully executed on PostgreSQL. Each Unsubscribe captures
	// the generation at call time and attaches it to the UNLISTEN command.
	// processPendingCmds compares the captured generation with the current one
	// and skips the UNLISTEN if they differ — meaning a newer LISTEN has
	// executed since the UNLISTEN was created.
	listenGen   map[string]uint64
	listenGenMu sync.Mutex

	// handlers are internal (backend-to-backend) callbacks invoked when a
	// NOTIFY arrives on a matching channel. Used for cross-pod cancellation.
	handlers   map[string]func(payload []byte)
	handlersMu sync.RWMutex

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
		listenGen:  make(map[string]uint64),
		handlers:   make(map[string]func(payload []byte)),
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
//
// Always sends LISTEN even if l.channels already marks the channel as active.
// PostgreSQL handles duplicate LISTEN idempotently. This prevents a race where
// a concurrent UNLISTEN goroutine (from unsubscribe) drops the LISTEN after
// this method's early-return check but before the goroutine executes.
//
// The per-channel generation counter is incremented by the receive loop when
// this LISTEN is actually executed on PostgreSQL (not here), ensuring any
// in-flight UNLISTEN from a prior Unsubscribe is detected as stale.
func (l *NotifyListener) Subscribe(ctx context.Context, channel string) error {
	if !l.running.Load() {
		return fmt.Errorf("LISTEN connection not established")
	}

	sanitized := pgx.Identifier{channel}.Sanitize()
	cmd := listenCmd{
		sql:     "LISTEN " + sanitized,
		channel: channel,
		result:  make(chan error, 1),
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
//
// The command carries the current generation counter. If a newer Subscribe has
// incremented the generation by the time the receive loop processes this command,
// the UNLISTEN is skipped as stale (see processPendingCmds).
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

	// Capture the current generation; processPendingCmds will skip this
	// UNLISTEN if a newer Subscribe has since incremented it.
	l.listenGenMu.Lock()
	gen := l.listenGen[channel]
	l.listenGenMu.Unlock()

	sanitized := pgx.Identifier{channel}.Sanitize()
	cmd := listenCmd{
		sql:     "UNLISTEN " + sanitized,
		channel: channel,
		gen:     gen,
		result:  make(chan error, 1),
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
		// Only remove from l.channels if no Subscribe raced us. If the
		// generation advanced, a newer LISTEN is active (or pending) and
		// the UNLISTEN was skipped by processPendingCmds — l.channels
		// must stay true so reconnect re-LISTENs the channel.
		l.listenGenMu.Lock()
		stale := l.listenGen[channel] != gen
		l.listenGenMu.Unlock()
		if !stale {
			l.channelsMu.Lock()
			delete(l.channels, channel)
			l.channelsMu.Unlock()
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// isListening reports whether the listener is actively LISTENing on the
// given channel. Unexported — used by tests to poll instead of sleeping.
func (l *NotifyListener) isListening(channel string) bool {
	l.channelsMu.RLock()
	defer l.channelsMu.RUnlock()
	return l.channels[channel]
}

// RegisterHandler registers an internal handler for a specific channel.
// When a NOTIFY arrives on that channel, the handler is invoked in addition
// to the normal ConnectionManager broadcast. Used for backend-to-backend
// communication (e.g., cross-pod session cancellation).
func (l *NotifyListener) RegisterHandler(channel string, fn func(payload []byte)) {
	l.handlersMu.Lock()
	defer l.handlersMu.Unlock()
	l.handlers[channel] = fn
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

		// Dispatch to internal handlers (backend-to-backend)
		l.handlersMu.RLock()
		handler := l.handlers[notification.Channel]
		l.handlersMu.RUnlock()
		if handler != nil {
			handler([]byte(notification.Payload))
		}

		// Dispatch to ConnectionManager (WebSocket clients)
		l.manager.Broadcast(notification.Channel, []byte(notification.Payload))
	}
}

// processPendingCmds drains the command channel and executes each
// LISTEN/UNLISTEN SQL command on the pgx connection.
//
// For LISTEN commands (cmd.gen == 0), the per-channel generation counter is
// incremented after successful execution. This ensures the generation only
// advances when the LISTEN actually runs on PostgreSQL.
//
// For UNLISTEN commands (cmd.gen > 0), the generation counter is compared
// with the current value. If a LISTEN has executed since the UNLISTEN was
// created, the generation will have advanced and the UNLISTEN is skipped —
// preventing a race where the cmdCh order LISTEN, UNLISTEN would leave the
// channel unlistened after a rapid unsubscribe/resubscribe cycle.
func (l *NotifyListener) processPendingCmds(ctx context.Context) {
	for {
		select {
		case cmd := <-l.cmdCh:
			// Drop stale UNLISTENs: if a LISTEN executed after this
			// Unsubscribe captured its generation, the UNLISTEN is obsolete.
			if cmd.gen > 0 {
				l.listenGenMu.Lock()
				stale := l.listenGen[cmd.channel] != cmd.gen
				l.listenGenMu.Unlock()
				if stale {
					cmd.result <- nil // no-op
					continue
				}
			}

			l.connMu.Lock()
			conn := l.conn
			l.connMu.Unlock()

			if conn == nil {
				cmd.result <- fmt.Errorf("LISTEN connection not established")
				continue
			}

			_, err := conn.Exec(ctx, cmd.sql)

			// Advance generation after a successful LISTEN so that any
			// UNLISTEN captured before this point becomes stale.
			if err == nil && cmd.gen == 0 && cmd.channel != "" {
				l.listenGenMu.Lock()
				l.listenGen[cmd.channel]++
				l.listenGenMu.Unlock()
			}

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
