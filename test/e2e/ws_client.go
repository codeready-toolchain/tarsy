package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// WSEvent represents a received WebSocket event.
type WSEvent struct {
	Type     string                 `json:"type"`
	Raw      json.RawMessage        // Original JSON
	Parsed   map[string]interface{} // Parsed for assertions
	Received time.Time              // When we received it
}

// WSClient connects to the TARSy WebSocket endpoint and collects events.
type WSClient struct {
	conn   *websocket.Conn
	events []WSEvent
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	doneCh chan struct{}
}

// WSConnect establishes a WebSocket connection to the test server and starts
// collecting events in a background goroutine.
func WSConnect(ctx context.Context, wsURL string) (*WSClient, error) {
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{})
	if err != nil {
		return nil, fmt.Errorf("WebSocket dial: %w", err)
	}

	clientCtx, cancel := context.WithCancel(ctx)
	c := &WSClient{
		conn:   conn,
		ctx:    clientCtx,
		cancel: cancel,
		doneCh: make(chan struct{}),
	}

	// Start background reader.
	go c.readLoop()

	return c, nil
}

// Subscribe sends a subscribe action for the given channel and waits for
// the server to confirm. This ensures the LISTEN + auto-catchup has completed
// before the caller proceeds, avoiding a race where events are checked before
// the server has delivered catchup events.
func (c *WSClient) Subscribe(channel string) error {
	msg := map[string]string{
		"action":  "subscribe",
		"channel": channel,
	}
	data, _ := json.Marshal(msg)
	if err := c.conn.Write(c.ctx, websocket.MessageText, data); err != nil {
		return err
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range c.Events() {
			if e.Type == "subscription.confirmed" && e.Parsed["channel"] == channel {
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for subscription.confirmed on channel %s", channel)
}

// Events returns a snapshot of all collected events.
func (c *WSClient) Events() []WSEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]WSEvent, len(c.events))
	copy(result, c.events)
	return result
}

// WaitForEvent polls the collected events until one matches the predicate or
// the timeout expires. This is preferred over time.Sleep for waiting on
// trailing WS events, as it adapts to CI load automatically.
func (c *WSClient) WaitForEvent(t interface {
	Helper()
	Fatalf(string, ...interface{})
}, match func(WSEvent) bool, timeout time.Duration, msgAndArgs ...interface{}) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, e := range c.Events() {
			if match(e) {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(msgAndArgs) > 0 {
		t.Fatalf(msgAndArgs[0].(string), msgAndArgs[1:]...)
	} else {
		t.Fatalf("WaitForEvent: timed out after %s waiting for matching WS event", timeout)
	}
}

// Close closes the WebSocket connection and waits for the read loop to exit.
func (c *WSClient) Close() error {
	c.cancel()
	_ = c.conn.CloseNow()
	<-c.doneCh
	return nil
}

// readLoop reads messages from the WebSocket and appends them to the events slice.
func (c *WSClient) readLoop() {
	defer close(c.doneCh)
	for {
		_, data, err := c.conn.Read(c.ctx)
		if err != nil {
			return // Connection closed or context cancelled.
		}

		var parsed map[string]interface{}
		if err := json.Unmarshal(data, &parsed); err != nil {
			continue // Skip malformed messages.
		}

		evt := WSEvent{
			Raw:      json.RawMessage(data),
			Parsed:   parsed,
			Received: time.Now(),
		}
		if t, ok := parsed["type"].(string); ok {
			evt.Type = t
		}

		c.mu.Lock()
		c.events = append(c.events, evt)
		c.mu.Unlock()
	}
}
