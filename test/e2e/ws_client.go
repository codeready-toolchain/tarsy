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

// Subscribe sends a subscribe action for the given channel.
func (c *WSClient) Subscribe(channel string) error {
	msg := map[string]string{
		"action":  "subscribe",
		"channel": channel,
	}
	data, _ := json.Marshal(msg)
	return c.conn.Write(c.ctx, websocket.MessageText, data)
}

// Events returns a snapshot of all collected events.
func (c *WSClient) Events() []WSEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]WSEvent, len(c.events))
	copy(result, c.events)
	return result
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
