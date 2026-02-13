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

// writeJSON marshals and writes a ClientMessage, failing the test on error.
func writeJSON(t *testing.T, conn *websocket.Conn, msg ClientMessage) {
	t.Helper()
	data, err := json.Marshal(msg)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, conn.Write(ctx, websocket.MessageText, data))
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
	writeJSON(t, conn, ClientMessage{Action: "subscribe", Channel: "session:test-123"})

	// Read subscription confirmation
	msg := readJSON(t, conn)
	assert.Equal(t, "subscription.confirmed", msg["type"])
	assert.Equal(t, "session:test-123", msg["channel"])

	// Verify active connections count
	require.Eventually(t, func() bool {
		return manager.ActiveConnections() == 1
	}, 2*time.Second, 10*time.Millisecond, "expected 1 active connection")
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
	writeJSON(t, conn1, ClientMessage{Action: "subscribe", Channel: channel})
	writeJSON(t, conn2, ClientMessage{Action: "subscribe", Channel: channel})

	// Read subscription confirmations
	readJSON(t, conn1)
	readJSON(t, conn2)

	// Wait for subscriptions to be fully registered
	require.Eventually(t, func() bool {
		return manager.subscriberCount(channel) == 2
	}, 2*time.Second, 10*time.Millisecond, "expected 2 subscribers")

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
	writeJSON(t, conn, ClientMessage{Action: "ping"})

	// Expect pong
	msg := readJSON(t, conn)
	assert.Equal(t, "pong", msg["type"])
}

func TestConnectionManager_CatchupOverflow(t *testing.T) {
	// Auto catch-up on subscribe with more events than the limit sends
	// catchupLimit events then a catchup.overflow message.
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

	// Subscribe — auto catch-up fires immediately
	writeJSON(t, conn, ClientMessage{Action: "subscribe", Channel: "session:overflow-test"})
	readJSON(t, conn) // subscription.confirmed

	// Read auto-catchup events (up to limit) then overflow message
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
	writeJSON(t, conn, ClientMessage{Action: "subscribe", Channel: channel})
	readJSON(t, conn) // subscription.confirmed

	require.Eventually(t, func() bool {
		return manager.subscriberCount(channel) == 1
	}, 2*time.Second, 10*time.Millisecond)

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

	// Subscribe to two channels
	writeJSON(t, conn, ClientMessage{Action: "subscribe", Channel: "session:ch1"})
	readJSON(t, conn) // subscription.confirmed

	writeJSON(t, conn, ClientMessage{Action: "subscribe", Channel: "session:ch2"})
	readJSON(t, conn) // subscription.confirmed

	require.Eventually(t, func() bool {
		return manager.subscriberCount("session:ch1") == 1 && manager.subscriberCount("session:ch2") == 1
	}, 2*time.Second, 10*time.Millisecond)

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

	channel := "session:unsub-test"

	// Subscribe
	writeJSON(t, conn, ClientMessage{Action: "subscribe", Channel: channel})
	readJSON(t, conn) // subscription.confirmed

	// Unsubscribe
	writeJSON(t, conn, ClientMessage{Action: "unsubscribe", Channel: channel})

	require.Eventually(t, func() bool {
		return manager.subscriberCount(channel) == 0
	}, 2*time.Second, 10*time.Millisecond)

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
	// Auto catch-up on subscribe: prior events are delivered in order
	// immediately after subscription.confirmed.
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

	// Subscribe — auto catch-up fires immediately after confirmation
	writeJSON(t, conn, ClientMessage{Action: "subscribe", Channel: "session:catchup-test"})
	readJSON(t, conn) // subscription.confirmed

	// Read all 3 auto-catchup events — each should have db_event_id injected
	for i := 0; i < 3; i++ {
		msg := readJSON(t, conn)
		assert.Equal(t, float64(i+1), msg["seq"])
		assert.NotNil(t, msg["db_event_id"], "catchup event should include db_event_id")
	}

	// No overflow should follow — try read with short timeout
	readCtx, readCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer readCancel()
	_, _, err := conn.Read(readCtx)
	assert.Error(t, err, "should not receive overflow message for small catchup")
}

func TestConnectionManager_CatchupError(t *testing.T) {
	// Catchup error (including auto-catchup on subscribe) should be logged
	// but not crash the connection. Connection remains usable.
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

	// Subscribe — auto catch-up fires and fails silently (DB error)
	writeJSON(t, conn, ClientMessage{Action: "subscribe", Channel: "session:err-test"})
	readJSON(t, conn) // subscription.confirmed

	// Connection should still be alive — ping/pong works
	writeJSON(t, conn, ClientMessage{Action: "ping"})
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

	// conn1 subscribes to ch1, conn2 subscribes to ch2
	writeJSON(t, conn1, ClientMessage{Action: "subscribe", Channel: "session:ch1"})
	readJSON(t, conn1) // subscription.confirmed

	writeJSON(t, conn2, ClientMessage{Action: "subscribe", Channel: "session:ch2"})
	readJSON(t, conn2) // subscription.confirmed

	require.Eventually(t, func() bool {
		return manager.subscriberCount("session:ch1") == 1 && manager.subscriberCount("session:ch2") == 1
	}, 2*time.Second, 10*time.Millisecond)

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

	// Subscribe with empty channel should return error
	writeJSON(t, conn, ClientMessage{Action: "subscribe", Channel: ""})
	msg := readJSON(t, conn)
	assert.Equal(t, "error", msg["type"])
	assert.Contains(t, msg["message"], "channel is required")

	// Unsubscribe with empty channel should return error
	writeJSON(t, conn, ClientMessage{Action: "unsubscribe", Channel: ""})
	msg = readJSON(t, conn)
	assert.Equal(t, "error", msg["type"])
	assert.Contains(t, msg["message"], "channel is required")

	// Catchup with empty channel should return error
	lastEventID := 0
	writeJSON(t, conn, ClientMessage{Action: "catchup", Channel: "", LastEventID: &lastEventID})
	msg = readJSON(t, conn)
	assert.Equal(t, "error", msg["type"])
	assert.Contains(t, msg["message"], "channel is required")

	// Connection should still be alive after validation errors
	writeJSON(t, conn, ClientMessage{Action: "ping"})
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

func TestConnectionManager_SubscribeListenFailure(t *testing.T) {
	// When LISTEN fails, subscribe should return subscription.error
	// instead of subscription.confirmed, and no catchup should be sent.
	events := []CatchupEvent{
		{ID: 1, Payload: map[string]interface{}{"type": "test"}},
	}
	manager := NewConnectionManager(&mockCatchupQuerier{events: events}, 5*time.Second)

	// Set a listener that was never started — Subscribe will fail with
	// "LISTEN connection not established".
	listener := NewNotifyListener("host=localhost", manager)
	manager.SetListener(listener)

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

	// Subscribe — LISTEN will fail
	writeJSON(t, conn, ClientMessage{Action: "subscribe", Channel: "session:listen-fail"})

	// Should receive subscription.error, NOT subscription.confirmed
	msg := readJSON(t, conn)
	assert.Equal(t, "subscription.error", msg["type"])
	assert.Equal(t, "session:listen-fail", msg["channel"])

	// Channel should not have any subscribers
	assert.Equal(t, 0, manager.subscriberCount("session:listen-fail"))

	// Connection should still be alive — ping/pong works
	writeJSON(t, conn, ClientMessage{Action: "ping"})
	msg = readJSON(t, conn)
	assert.Equal(t, "pong", msg["type"])
}

func TestConnectionManager_SubscribeListenFailure_CleansUpOrphanedSubscribers(t *testing.T) {
	// When LISTEN fails, other connections that subscribed to the same channel
	// between the channelMu unlock and the LISTEN call must be removed from
	// m.channels and notified with subscription.error.
	//
	// Notification via real WebSockets is exercised by
	// TestConnectionManager_SubscribeListenFailure; here we verify that the
	// channel map is cleaned up for ALL subscribers (not just the triggering one).
	manager := NewConnectionManager(&mockCatchupQuerier{}, 5*time.Second)

	channel := "session:orphan-test"

	// Create fake connections. We only register connA in manager.connections;
	// connB and connC are placed in the channel map to simulate the race, but
	// are not in manager.connections — so cleanupFailedChannel won't attempt to
	// send to them (avoiding nil-Conn panics). The important assertion is that
	// the entire channel entry is deleted, not just the triggering connection.
	connA := &Connection{ID: "conn-a", subscriptions: make(map[string]bool)}

	manager.mu.Lock()
	manager.connections[connA.ID] = connA
	manager.mu.Unlock()

	// Simulate the state after all three subscribed but before LISTEN completes:
	// - Channel exists in m.channels with all three connection IDs
	manager.channelMu.Lock()
	manager.channels[channel] = map[string]bool{
		connA.ID: true,
		"conn-b": true,
		"conn-c": true,
	}
	manager.channelMu.Unlock()

	// Now simulate LISTEN failure: call cleanupFailedChannel as subscribe would.
	manager.cleanupFailedChannel(connA, channel)

	// Channel should be completely removed from m.channels — not just connA.
	assert.Equal(t, 0, manager.subscriberCount(channel),
		"channel should have zero subscribers after cleanup")

	manager.channelMu.RLock()
	_, exists := manager.channels[channel]
	manager.channelMu.RUnlock()
	assert.False(t, exists, "channel entry should be deleted from m.channels")
}

func TestConnectionManager_SubscribeListenFailure_NotifiesOrphanedSubscribers(t *testing.T) {
	// End-to-end test: two real WebSocket clients each subscribe to the same
	// channel backed by a listener whose LISTEN always fails. Both should
	// receive subscription.error and the channel should have zero subscribers.
	events := []CatchupEvent{
		{ID: 1, Payload: map[string]interface{}{"type": "test"}},
	}
	manager := NewConnectionManager(&mockCatchupQuerier{events: events}, 5*time.Second)

	// Listener whose Subscribe always fails.
	listener := NewNotifyListener("host=localhost", manager)
	manager.SetListener(listener)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		manager.HandleConnection(r.Context(), conn)
	}))
	defer server.Close()

	// Connect first client and subscribe — this triggers the (failing) LISTEN.
	conn1 := connectWS(t, server)
	readJSON(t, conn1) // connection.established
	writeJSON(t, conn1, ClientMessage{Action: "subscribe", Channel: "session:orphan-ws"})

	msg1 := readJSON(t, conn1)
	assert.Equal(t, "subscription.error", msg1["type"],
		"first client should receive subscription.error")

	// Connect second client and subscribe — triggers another (failing) LISTEN
	// because the channel was cleaned up after the first failure.
	conn2 := connectWS(t, server)
	readJSON(t, conn2) // connection.established
	writeJSON(t, conn2, ClientMessage{Action: "subscribe", Channel: "session:orphan-ws"})

	msg2 := readJSON(t, conn2)
	assert.Equal(t, "subscription.error", msg2["type"],
		"second client should receive subscription.error")

	// Channel should have zero subscribers after both failures.
	assert.Equal(t, 0, manager.subscriberCount("session:orphan-ws"))

	// Both connections should still be alive.
	writeJSON(t, conn1, ClientMessage{Action: "ping"})
	pong1 := readJSON(t, conn1)
	assert.Equal(t, "pong", pong1["type"], "conn1 should still be alive")

	writeJSON(t, conn2, ClientMessage{Action: "ping"})
	pong2 := readJSON(t, conn2)
	assert.Equal(t, "pong", pong2["type"], "conn2 should still be alive")
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
	require.NoError(t, conn.Write(ctx, websocket.MessageText, subMsg))
	_, _, err = conn.Read(ctx) // subscription.confirmed
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return manager.ActiveConnections() == 1
	}, 2*time.Second, 10*time.Millisecond, "expected 1 active connection")

	// Close the connection
	conn.Close(websocket.StatusNormalClosure, "")

	// Connection should be cleaned up
	require.Eventually(t, func() bool {
		return manager.ActiveConnections() == 0
	}, 2*time.Second, 10*time.Millisecond, "expected 0 active connections after close")

	// Broadcast should not panic
	payload, _ := json.Marshal(map[string]string{"type": "test"})
	assert.NotPanics(t, func() {
		manager.Broadcast("session:cleanup-test", payload)
	})
}
