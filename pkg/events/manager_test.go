package events

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockCatchupQuerier implements CatchupQuerier for tests.
type mockCatchupQuerier struct {
	events []CatchupEvent
	err    error
}

func (m *mockCatchupQuerier) GetCatchupEvents(_ context.Context, _ string, _ int, limit int) ([]CatchupEvent, error) {
	if m.err != nil {
		return nil, m.err
	}
	if limit > 0 && len(m.events) > limit {
		return m.events[:limit], nil
	}
	return m.events, nil
}

func setupTestManager(t *testing.T) (*ConnectionManager, *httptest.Server) {
	t.Helper()

	manager := NewConnectionManager(&mockCatchupQuerier{}, 5*time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			t.Logf("WebSocket accept error: %v", err)
			return
		}
		manager.HandleConnection(r.Context(), conn)
	}))

	t.Cleanup(func() { server.Close() })
	return manager, server
}

func connectWS(t *testing.T, server *httptest.Server) *websocket.Conn {
	t.Helper()
	url := "ws" + server.URL[len("http"):]
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, url, nil)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
	return conn
}

func readJSON(t *testing.T, conn *websocket.Conn) map[string]interface{} {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, data, err := conn.Read(ctx)
	require.NoError(t, err)

	var msg map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &msg))
	return msg
}

func TestConnectionManager_ConnectionEstablished(t *testing.T) {
	_, server := setupTestManager(t)
	conn := connectWS(t, server)

	msg := readJSON(t, conn)
	assert.Equal(t, "connection.established", msg["type"])
	assert.NotEmpty(t, msg["connection_id"])
}

func TestConnectionManager_SubscribeUnsubscribe(t *testing.T) {
	manager, server := setupTestManager(t)
	conn := connectWS(t, server)

	// Read connection.established
	readJSON(t, conn)

	// Subscribe
	ctx := context.Background()
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	subMsg, _ := json.Marshal(ClientMessage{Action: "subscribe", Channel: "session:test-123"})
	err := conn.Write(writeCtx, websocket.MessageText, subMsg)
	require.NoError(t, err)

	// Read subscription confirmation
	msg := readJSON(t, conn)
	assert.Equal(t, "subscription.confirmed", msg["type"])
	assert.Equal(t, "session:test-123", msg["channel"])

	// Verify active connections count
	time.Sleep(50 * time.Millisecond) // Let subscription propagate
	assert.Equal(t, 1, manager.ActiveConnections())
}

func TestConnectionManager_Broadcast(t *testing.T) {
	manager, server := setupTestManager(t)

	// Connect two clients and subscribe both to same channel
	conn1 := connectWS(t, server)
	conn2 := connectWS(t, server)

	// Read connection.established for both
	readJSON(t, conn1)
	readJSON(t, conn2)

	// Subscribe both to the same channel
	channel := "session:broadcast-test"
	subMsg, _ := json.Marshal(ClientMessage{Action: "subscribe", Channel: channel})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn1.Write(ctx, websocket.MessageText, subMsg)
	conn2.Write(ctx, websocket.MessageText, subMsg)

	// Read subscription confirmations
	readJSON(t, conn1)
	readJSON(t, conn2)

	// Wait for subscriptions to propagate
	time.Sleep(100 * time.Millisecond)

	// Broadcast a message
	payload, _ := json.Marshal(map[string]string{"type": "test", "data": "hello"})
	manager.Broadcast(channel, payload)

	// Both clients should receive the message
	msg1 := readJSON(t, conn1)
	msg2 := readJSON(t, conn2)

	assert.Equal(t, "test", msg1["type"])
	assert.Equal(t, "hello", msg1["data"])
	assert.Equal(t, "test", msg2["type"])
	assert.Equal(t, "hello", msg2["data"])
}

func TestConnectionManager_PingPong(t *testing.T) {
	_, server := setupTestManager(t)
	conn := connectWS(t, server)

	// Read connection.established
	readJSON(t, conn)

	// Send ping
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pingMsg, _ := json.Marshal(ClientMessage{Action: "ping"})
	err := conn.Write(ctx, websocket.MessageText, pingMsg)
	require.NoError(t, err)

	// Expect pong
	msg := readJSON(t, conn)
	assert.Equal(t, "pong", msg["type"])
}

func TestConnectionManager_CatchupOverflow(t *testing.T) {
	// Create querier that returns more events than catchup limit
	manyEvents := make([]CatchupEvent, catchupLimit+5)
	for i := range manyEvents {
		manyEvents[i] = CatchupEvent{
			ID: i + 1,
			Payload: map[string]interface{}{
				"type": "test",
				"seq":  i,
			},
		}
	}

	manager := NewConnectionManager(&mockCatchupQuerier{events: manyEvents}, 5*time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			return
		}
		manager.HandleConnection(r.Context(), conn)
	}))
	defer server.Close()

	conn := connectWS(t, server)
	readJSON(t, conn) // connection.established

	// Subscribe
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	subMsg, _ := json.Marshal(ClientMessage{Action: "subscribe", Channel: "session:overflow-test"})
	conn.Write(ctx, websocket.MessageText, subMsg)
	readJSON(t, conn) // subscription.confirmed

	time.Sleep(100 * time.Millisecond)

	// Request catchup
	lastEventID := 0
	catchupMsg, _ := json.Marshal(ClientMessage{Action: "catchup", Channel: "session:overflow-test", LastEventID: &lastEventID})
	conn.Write(ctx, websocket.MessageText, catchupMsg)

	// Read catchup events (up to limit) and then overflow message
	var overflowReceived bool
	for i := 0; i < catchupLimit+5; i++ {
		msg := readJSON(t, conn)
		if msg["type"] == "catchup.overflow" {
			overflowReceived = true
			assert.Equal(t, true, msg["has_more"])
			break
		}
	}
	assert.True(t, overflowReceived, "expected catchup.overflow message")
}

func TestConnectionManager_ConcurrentBroadcast(t *testing.T) {
	manager, server := setupTestManager(t)
	conn := connectWS(t, server)
	readJSON(t, conn) // connection.established

	channel := "session:concurrent-test"
	subMsg, _ := json.Marshal(ClientMessage{Action: "subscribe", Channel: channel})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn.Write(ctx, websocket.MessageText, subMsg)
	readJSON(t, conn) // subscription.confirmed

	time.Sleep(100 * time.Millisecond)

	// Broadcast 20 messages concurrently
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			payload, _ := json.Marshal(map[string]interface{}{"type": "concurrent", "idx": idx})
			manager.Broadcast(channel, payload)
		}(i)
	}
	wg.Wait()

	// Read all 20 messages (order may vary due to concurrency)
	received := 0
	var firstErr error
	for i := 0; i < 20; i++ {
		readCtx, readCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _, err := conn.Read(readCtx)
		readCancel()
		if err != nil {
			firstErr = err
			break
		}
		received++
	}
	assert.Equal(t, 20, received, "should receive all 20 broadcast messages; first error: %v", firstErr)
}

func TestConnectionManager_BroadcastToNonExistentChannel(t *testing.T) {
	manager, _ := setupTestManager(t)

	// Should not panic
	payload, _ := json.Marshal(map[string]string{"type": "test"})
	manager.Broadcast("nonexistent-channel", payload)
}

func TestConnectionManager_MultipleChannels(t *testing.T) {
	manager, server := setupTestManager(t)
	conn := connectWS(t, server)
	readJSON(t, conn) // connection.established

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Subscribe to two channels
	subMsg1, _ := json.Marshal(ClientMessage{Action: "subscribe", Channel: "session:ch1"})
	conn.Write(ctx, websocket.MessageText, subMsg1)
	readJSON(t, conn) // subscription.confirmed

	subMsg2, _ := json.Marshal(ClientMessage{Action: "subscribe", Channel: "session:ch2"})
	conn.Write(ctx, websocket.MessageText, subMsg2)
	readJSON(t, conn) // subscription.confirmed

	time.Sleep(100 * time.Millisecond)

	// Broadcast to channel 1 only
	payload, _ := json.Marshal(map[string]string{"type": "test", "channel": "ch1"})
	manager.Broadcast("session:ch1", payload)

	msg := readJSON(t, conn)
	assert.Equal(t, "ch1", msg["channel"])

	// Broadcast to channel 2 only
	payload2, _ := json.Marshal(map[string]string{"type": "test", "channel": "ch2"})
	manager.Broadcast("session:ch2", payload2)

	msg2 := readJSON(t, conn)
	assert.Equal(t, "ch2", msg2["channel"])
}

func TestConnectionManager_Unsubscribe(t *testing.T) {
	manager, server := setupTestManager(t)
	conn := connectWS(t, server)
	readJSON(t, conn) // connection.established

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	channel := "session:unsub-test"

	// Subscribe
	subMsg, _ := json.Marshal(ClientMessage{Action: "subscribe", Channel: channel})
	conn.Write(ctx, websocket.MessageText, subMsg)
	readJSON(t, conn) // subscription.confirmed

	// Unsubscribe
	unsubMsg, _ := json.Marshal(ClientMessage{Action: "unsubscribe", Channel: channel})
	conn.Write(ctx, websocket.MessageText, unsubMsg)

	time.Sleep(100 * time.Millisecond)

	// Broadcast — should NOT be received
	payload, _ := json.Marshal(map[string]string{"type": "should-not-receive"})
	manager.Broadcast(channel, payload)

	// Try to read — should timeout
	readCtx, readCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer readCancel()

	_, _, err := conn.Read(readCtx)
	assert.Error(t, err, "should not receive message after unsubscribe")
}

func TestConnectionManager_CatchupNormal(t *testing.T) {
	// Normal catchup: events under the limit are delivered in order
	events := []CatchupEvent{
		{ID: 10, Payload: map[string]interface{}{"type": "timeline_event.created", "seq": float64(1)}},
		{ID: 11, Payload: map[string]interface{}{"type": "stream.chunk", "seq": float64(2)}},
		{ID: 12, Payload: map[string]interface{}{"type": "timeline_event.completed", "seq": float64(3)}},
	}

	manager := NewConnectionManager(&mockCatchupQuerier{events: events}, 5*time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		manager.HandleConnection(r.Context(), conn)
	}))
	defer server.Close()

	conn := connectWS(t, server)
	readJSON(t, conn) // connection.established

	// Subscribe
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	subMsg, _ := json.Marshal(ClientMessage{Action: "subscribe", Channel: "session:catchup-test"})
	conn.Write(ctx, websocket.MessageText, subMsg)
	readJSON(t, conn) // subscription.confirmed

	time.Sleep(100 * time.Millisecond)

	// Request catchup from event 0 — should receive all 3 events, no overflow
	lastEventID := 0
	catchupMsg, _ := json.Marshal(ClientMessage{Action: "catchup", Channel: "session:catchup-test", LastEventID: &lastEventID})
	conn.Write(ctx, websocket.MessageText, catchupMsg)

	// Read all 3 catchup events
	for i := 0; i < 3; i++ {
		msg := readJSON(t, conn)
		assert.Equal(t, float64(i+1), msg["seq"])
	}

	// No overflow should follow — try read with short timeout
	readCtx, readCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer readCancel()
	_, _, err := conn.Read(readCtx)
	assert.Error(t, err, "should not receive overflow message for small catchup")
}

func TestConnectionManager_CatchupError(t *testing.T) {
	// Catchup error should be logged but not crash the connection.
	// Verify the connection remains usable after a catchup query failure.
	manager := NewConnectionManager(&mockCatchupQuerier{err: fmt.Errorf("database unreachable")}, 5*time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		manager.HandleConnection(r.Context(), conn)
	}))
	defer server.Close()

	conn := connectWS(t, server)
	readJSON(t, conn) // connection.established

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	subMsg, _ := json.Marshal(ClientMessage{Action: "subscribe", Channel: "session:err-test"})
	conn.Write(ctx, websocket.MessageText, subMsg)
	readJSON(t, conn) // subscription.confirmed

	time.Sleep(100 * time.Millisecond)

	// Request catchup — error should be silently handled
	lastEventID := 0
	catchupMsg, _ := json.Marshal(ClientMessage{Action: "catchup", Channel: "session:err-test", LastEventID: &lastEventID})
	conn.Write(ctx, websocket.MessageText, catchupMsg)

	// Give server time to process catchup and log error
	time.Sleep(100 * time.Millisecond)

	// Connection should still be alive — ping/pong works
	pingMsg, _ := json.Marshal(ClientMessage{Action: "ping"})
	conn.Write(ctx, websocket.MessageText, pingMsg)
	msg := readJSON(t, conn)
	assert.Equal(t, "pong", msg["type"])
}

func TestConnectionManager_BroadcastIsolation(t *testing.T) {
	// Client subscribed to ch1 should NOT receive ch2 broadcasts
	manager, server := setupTestManager(t)

	conn1 := connectWS(t, server)
	conn2 := connectWS(t, server)
	readJSON(t, conn1) // connection.established
	readJSON(t, conn2) // connection.established

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// conn1 subscribes to ch1, conn2 subscribes to ch2
	sub1, _ := json.Marshal(ClientMessage{Action: "subscribe", Channel: "session:ch1"})
	conn1.Write(ctx, websocket.MessageText, sub1)
	readJSON(t, conn1) // subscription.confirmed

	sub2, _ := json.Marshal(ClientMessage{Action: "subscribe", Channel: "session:ch2"})
	conn2.Write(ctx, websocket.MessageText, sub2)
	readJSON(t, conn2) // subscription.confirmed

	time.Sleep(100 * time.Millisecond)

	// Broadcast to ch1 — only conn1 should receive
	payload1, _ := json.Marshal(map[string]string{"type": "test", "target": "ch1"})
	manager.Broadcast("session:ch1", payload1)

	msg := readJSON(t, conn1)
	assert.Equal(t, "ch1", msg["target"])

	// conn2 should NOT receive ch1's message — verify with timeout
	readCtx, readCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer readCancel()
	_, _, err := conn2.Read(readCtx)
	assert.Error(t, err, "conn2 should not receive ch1 broadcast")
}

func TestConnectionManager_EmptyChannelValidation(t *testing.T) {
	_, server := setupTestManager(t)
	conn := connectWS(t, server)
	readJSON(t, conn) // connection.established

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Subscribe with empty channel should return error
	subMsg, _ := json.Marshal(ClientMessage{Action: "subscribe", Channel: ""})
	conn.Write(ctx, websocket.MessageText, subMsg)
	msg := readJSON(t, conn)
	assert.Equal(t, "error", msg["type"])
	assert.Contains(t, msg["message"], "channel is required")

	// Unsubscribe with empty channel should return error
	unsubMsg, _ := json.Marshal(ClientMessage{Action: "unsubscribe", Channel: ""})
	conn.Write(ctx, websocket.MessageText, unsubMsg)
	msg = readJSON(t, conn)
	assert.Equal(t, "error", msg["type"])
	assert.Contains(t, msg["message"], "channel is required")

	// Catchup with empty channel should return error
	lastEventID := 0
	catchupMsg, _ := json.Marshal(ClientMessage{Action: "catchup", Channel: "", LastEventID: &lastEventID})
	conn.Write(ctx, websocket.MessageText, catchupMsg)
	msg = readJSON(t, conn)
	assert.Equal(t, "error", msg["type"])
	assert.Contains(t, msg["message"], "channel is required")

	// Connection should still be alive after validation errors
	pingMsg, _ := json.Marshal(ClientMessage{Action: "ping"})
	conn.Write(ctx, websocket.MessageText, pingMsg)
	msg = readJSON(t, conn)
	assert.Equal(t, "pong", msg["type"])
}

func TestConnectionManager_SetListener(t *testing.T) {
	manager := NewConnectionManager(&mockCatchupQuerier{}, 5*time.Second)
	assert.Nil(t, manager.listener)

	listener := NewNotifyListener("host=localhost", manager)
	manager.SetListener(listener)

	manager.listenerMu.RLock()
	assert.Equal(t, listener, manager.listener)
	manager.listenerMu.RUnlock()
}

func TestConnectionManager_CleanupOnDisconnect(t *testing.T) {
	manager, server := setupTestManager(t)

	// Connect and subscribe
	url := "ws" + server.URL[len("http"):]
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, url, nil)
	require.NoError(t, err)

	// Read connection.established
	_, _, err = conn.Read(ctx)
	require.NoError(t, err)

	// Subscribe
	subMsg, _ := json.Marshal(ClientMessage{Action: "subscribe", Channel: "session:cleanup-test"})
	conn.Write(ctx, websocket.MessageText, subMsg)
	_, _, err = conn.Read(ctx) // subscription.confirmed
	require.NoError(t, err)

	assert.Equal(t, 1, manager.ActiveConnections())

	// Close the connection
	conn.Close(websocket.StatusNormalClosure, "")
	time.Sleep(200 * time.Millisecond)

	// Connection should be cleaned up
	assert.Equal(t, 0, manager.ActiveConnections())

	// Broadcast should not panic
	payload, _ := json.Marshal(map[string]string{"type": "test"})
	assert.NotPanics(t, func() {
		manager.Broadcast("session:cleanup-test", payload)
	})
}
