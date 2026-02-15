package events

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/database"
	"github.com/codeready-toolchain/tarsy/pkg/services"
	testdb "github.com/codeready-toolchain/tarsy/test/database"
	"github.com/codeready-toolchain/tarsy/test/util"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// streamingTestEnv holds all wired-up components for an integration test.
type streamingTestEnv struct {
	dbClient     *database.Client
	publisher    *EventPublisher
	eventService *services.EventService
	manager      *ConnectionManager
	listener     *NotifyListener
	server       *httptest.Server
	sessionID    string // Pre-created AlertSession (satisfies FK on events)
	channel      string // session:<sessionID>
}

// setupStreamingTest wires all real components together against a real
// PostgreSQL database (testcontainers locally, service container in CI).
func setupStreamingTest(t *testing.T) *streamingTestEnv {
	t.Helper()

	dbClient := testdb.NewTestClient(t)
	ctx := context.Background()

	// Create AlertSession required by FK on events table
	sessionID := uuid.New().String()
	_, err := dbClient.AlertSession.Create().
		SetID(sessionID).
		SetAlertData("integration test alert").
		SetAgentType("test-agent").
		SetAlertType("test-alert").
		SetChainID("test-chain").
		SetStatus(alertsession.StatusPending).
		SetAuthor("integration-test").
		Save(ctx)
	require.NoError(t, err)

	channel := SessionChannel(sessionID)

	// Real components
	publisher := NewEventPublisher(dbClient.DB())
	eventService := services.NewEventService(dbClient.Client)
	catchupQuerier := NewEventServiceAdapter(eventService)
	manager := NewConnectionManager(catchupQuerier, 5*time.Second)

	// NotifyListener needs the base connection string (no schema search_path)
	// because NOTIFY/LISTEN is database-level, not schema-level.
	baseConnStr := util.GetBaseConnectionString(t)
	listener := NewNotifyListener(baseConnStr, manager)
	require.NoError(t, listener.Start(ctx))
	manager.SetListener(listener)

	t.Cleanup(func() { listener.Stop(context.Background()) })

	// httptest server with WebSocket upgrade
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

	return &streamingTestEnv{
		dbClient:     dbClient,
		publisher:    publisher,
		eventService: eventService,
		manager:      manager,
		listener:     listener,
		server:       server,
		sessionID:    sessionID,
		channel:      channel,
	}
}

// connectIntegrationWS opens a WebSocket to the test server and returns
// the connection. The connection is automatically closed on test cleanup.
func (env *streamingTestEnv) connectWS(t *testing.T) *websocket.Conn {
	t.Helper()
	url := "ws" + env.server.URL[len("http"):]
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, url, nil)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
	return conn
}

// readJSONTimeout reads a JSON message from the WebSocket with a timeout.
func readJSONTimeout(t *testing.T, conn *websocket.Conn, timeout time.Duration) map[string]interface{} {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	_, data, err := conn.Read(ctx)
	require.NoError(t, err)

	var msg map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &msg))
	return msg
}

// subscribeAndWait connects a WebSocket, reads connection.established,
// subscribes to the env's channel, reads subscription.confirmed, and
// waits for the LISTEN to propagate.
func (env *streamingTestEnv) subscribeAndWait(t *testing.T) *websocket.Conn {
	t.Helper()
	conn := env.connectWS(t)

	// Read connection.established
	msg := readJSONTimeout(t, conn, 5*time.Second)
	require.Equal(t, "connection.established", msg["type"])

	// Subscribe
	subMsg, _ := json.Marshal(ClientMessage{Action: "subscribe", Channel: env.channel})
	writeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, conn.Write(writeCtx, websocket.MessageText, subMsg))

	// Read subscription.confirmed
	msg = readJSONTimeout(t, conn, 5*time.Second)
	require.Equal(t, "subscription.confirmed", msg["type"])

	// Wait for the async LISTEN goroutine to complete on the NotifyListener's
	// dedicated connection, polling instead of sleeping.
	require.Eventually(t, func() bool {
		return env.listener.isListening(env.channel)
	}, 2*time.Second, 10*time.Millisecond, "LISTEN did not propagate for channel %s", env.channel)

	return conn
}

// --- Tests ---

func TestIntegration_PublisherPersistsAndNotifies(t *testing.T) {
	env := setupStreamingTest(t)
	ctx := context.Background()

	// Publish first event (timeline created)
	err := env.publisher.PublishTimelineCreated(ctx, env.sessionID, TimelineCreatedPayload{
		BasePayload: BasePayload{
			Type:      EventTypeTimelineCreated,
			SessionID: env.sessionID,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		},
		EventID: "evt-1",
		Content: "first event",
	})
	require.NoError(t, err)

	// Publish second event (timeline completed)
	err = env.publisher.PublishTimelineCompleted(ctx, env.sessionID, TimelineCompletedPayload{
		BasePayload: BasePayload{
			Type:      EventTypeTimelineCompleted,
			SessionID: env.sessionID,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		},
		EventID:   "evt-1",
		EventType: timelineevent.EventTypeLlmResponse,
		Content:   "second event",
		Status:    timelineevent.StatusCompleted,
	})
	require.NoError(t, err)

	// Query persisted events via EventService
	events, err := env.eventService.GetEventsSince(ctx, env.channel, 0, 100)
	require.NoError(t, err)
	require.Len(t, events, 2)

	// Verify order and content
	assert.Equal(t, env.sessionID, events[0].SessionID)
	assert.Equal(t, env.channel, events[0].Channel)
	assert.Equal(t, EventTypeTimelineCreated, events[0].Payload["type"])
	assert.Equal(t, "first event", events[0].Payload["content"])

	assert.Equal(t, EventTypeTimelineCompleted, events[1].Payload["type"])
	assert.Equal(t, "second event", events[1].Payload["content"])
	assert.Equal(t, "llm_response", events[1].Payload["event_type"], "completed event should persist event_type")

	// IDs should be incrementing
	assert.Greater(t, events[1].ID, events[0].ID)
}

func TestIntegration_TransientEventsNotPersisted(t *testing.T) {
	env := setupStreamingTest(t)
	ctx := context.Background()

	// Publish transient event (stream chunk)
	err := env.publisher.PublishStreamChunk(ctx, env.sessionID, StreamChunkPayload{
		BasePayload: BasePayload{
			Type:      EventTypeStreamChunk,
			SessionID: env.sessionID,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		},
		EventID: "evt-1",
		Delta:   "token data",
	})
	require.NoError(t, err)

	// Query DB — should have zero persisted events
	events, err := env.eventService.GetEventsSince(ctx, env.channel, 0, 100)
	require.NoError(t, err)
	assert.Empty(t, events, "transient events should not be persisted in DB")
}

func TestIntegration_EndToEnd_PublishToWebSocket(t *testing.T) {
	env := setupStreamingTest(t)
	ctx := context.Background()

	// Connect, subscribe, and wait for LISTEN to propagate
	conn := env.subscribeAndWait(t)

	// Publish a persistent event via EventPublisher
	err := env.publisher.PublishTimelineCreated(ctx, env.sessionID, TimelineCreatedPayload{
		BasePayload: BasePayload{
			Type:      EventTypeTimelineCreated,
			SessionID: env.sessionID,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		},
		EventID: "evt-ws-1",
		Content: "hello from publisher",
	})
	require.NoError(t, err)

	// Read from WebSocket — the event should arrive via pg_notify → listener → manager
	msg := readJSONTimeout(t, conn, 5*time.Second)
	assert.Equal(t, EventTypeTimelineCreated, msg["type"])
	assert.Equal(t, "hello from publisher", msg["content"])
	assert.Equal(t, env.sessionID, msg["session_id"])
	// db_event_id should be present (added by persistAndNotify after INSERT)
	assert.NotNil(t, msg["db_event_id"])
}

func TestIntegration_TransientEventDelivery(t *testing.T) {
	env := setupStreamingTest(t)
	ctx := context.Background()

	// Connect, subscribe, wait for LISTEN
	conn := env.subscribeAndWait(t)

	// Publish transient event (no DB persistence)
	err := env.publisher.PublishStreamChunk(ctx, env.sessionID, StreamChunkPayload{
		BasePayload: BasePayload{
			Type:      EventTypeStreamChunk,
			SessionID: env.sessionID,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		},
		EventID: "evt-stream-1",
		Delta:   "streaming token",
	})
	require.NoError(t, err)

	// Should arrive via WebSocket
	msg := readJSONTimeout(t, conn, 5*time.Second)
	assert.Equal(t, EventTypeStreamChunk, msg["type"])
	assert.Equal(t, "streaming token", msg["delta"])

	// Verify nothing was persisted
	events, err := env.eventService.GetEventsSince(ctx, env.channel, 0, 100)
	require.NoError(t, err)
	assert.Empty(t, events, "transient events should not be persisted")
}

func TestIntegration_DeltaStreamingProtocol(t *testing.T) {
	// Verifies the full delta streaming protocol:
	// 1. timeline_event.created (persistent, status=streaming)
	// 2. stream.chunk deltas (transient, small payloads)
	// 3. timeline_event.completed (persistent, full content)
	// The client must concatenate deltas to reconstruct the content.
	env := setupStreamingTest(t)
	ctx := context.Background()

	conn := env.subscribeAndWait(t)

	eventID := uuid.New().String()

	// 1. Publish timeline_event.created (persistent)
	err := env.publisher.PublishTimelineCreated(ctx, env.sessionID, TimelineCreatedPayload{
		BasePayload: BasePayload{
			Type:      EventTypeTimelineCreated,
			SessionID: env.sessionID,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		},
		EventID:   eventID,
		EventType: "llm_response",
		Status:    timelineevent.StatusStreaming,
		Content:   "",
	})
	require.NoError(t, err)

	msg := readJSONTimeout(t, conn, 5*time.Second)
	assert.Equal(t, EventTypeTimelineCreated, msg["type"])
	assert.Equal(t, eventID, msg["event_id"])
	assert.Equal(t, "streaming", msg["status"])

	// 2. Publish multiple stream.chunk deltas (transient)
	deltas := []string{"The pod ", "is in ", "CrashLoopBackOff ", "due to ", "a missing ConfigMap."}
	for _, delta := range deltas {
		err := env.publisher.PublishStreamChunk(ctx, env.sessionID, StreamChunkPayload{
			BasePayload: BasePayload{
				Type:      EventTypeStreamChunk,
				SessionID: env.sessionID,
				Timestamp: time.Now().Format(time.RFC3339Nano),
			},
			EventID: eventID,
			Delta:   delta,
		})
		require.NoError(t, err)

		msg := readJSONTimeout(t, conn, 5*time.Second)
		assert.Equal(t, EventTypeStreamChunk, msg["type"])
		assert.Equal(t, eventID, msg["event_id"])
		assert.Equal(t, delta, msg["delta"], "each chunk should carry only the new delta")
	}

	// Client-side reconstruction: concatenating all deltas
	var reconstructed string
	for _, d := range deltas {
		reconstructed += d
	}
	expectedFull := "The pod is in CrashLoopBackOff due to a missing ConfigMap."
	assert.Equal(t, expectedFull, reconstructed)

	// 3. Publish timeline_event.completed (persistent, full content)
	err = env.publisher.PublishTimelineCompleted(ctx, env.sessionID, TimelineCompletedPayload{
		BasePayload: BasePayload{
			Type:      EventTypeTimelineCompleted,
			SessionID: env.sessionID,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		},
		EventID:   eventID,
		EventType: timelineevent.EventTypeLlmResponse,
		Content:   expectedFull,
		Status:    timelineevent.StatusCompleted,
	})
	require.NoError(t, err)

	msg = readJSONTimeout(t, conn, 5*time.Second)
	assert.Equal(t, EventTypeTimelineCompleted, msg["type"])
	assert.Equal(t, expectedFull, msg["content"])
	assert.Equal(t, "completed", msg["status"])
	assert.Equal(t, "llm_response", msg["event_type"], "completed WS message must include event_type")

	// Only the 2 persistent events should be in DB (created + completed)
	// The 5 stream.chunk deltas are transient — not persisted
	events, err := env.eventService.GetEventsSince(ctx, env.channel, 0, 100)
	require.NoError(t, err)
	assert.Len(t, events, 2, "only persistent events should be in DB")
	assert.Equal(t, EventTypeTimelineCreated, events[0].Payload["type"])
	assert.Equal(t, EventTypeTimelineCompleted, events[1].Payload["type"])
	assert.Equal(t, "llm_response", events[1].Payload["event_type"], "completed DB record must include event_type")
}

func TestIntegration_CatchupFromRealDB(t *testing.T) {
	env := setupStreamingTest(t)
	ctx := context.Background()

	// Pre-populate DB with 3 persistent events
	for i := 1; i <= 3; i++ {
		err := env.publisher.PublishTimelineCreated(ctx, env.sessionID, TimelineCreatedPayload{
			BasePayload: BasePayload{
				Type:      EventTypeTimelineCreated,
				SessionID: env.sessionID,
				Timestamp: time.Now().Format(time.RFC3339Nano),
			},
			EventID:        uuid.New().String(),
			SequenceNumber: i,
		})
		require.NoError(t, err)
	}

	// Verify events exist in DB
	allEvents, err := env.eventService.GetEventsSince(ctx, env.channel, 0, 100)
	require.NoError(t, err)
	require.Len(t, allEvents, 3)
	firstEventID := allEvents[0].ID

	// Connect a NEW WebSocket client (simulates reconnection)
	conn := env.connectWS(t)
	msg := readJSONTimeout(t, conn, 5*time.Second) // connection.established
	require.Equal(t, "connection.established", msg["type"])

	// Subscribe — auto-catchup delivers all 3 prior events immediately
	subMsg, _ := json.Marshal(ClientMessage{Action: "subscribe", Channel: env.channel})
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	require.NoError(t, conn.Write(writeCtx, websocket.MessageText, subMsg))
	msg = readJSONTimeout(t, conn, 5*time.Second) // subscription.confirmed
	require.Equal(t, "subscription.confirmed", msg["type"])

	// Read all 3 auto-catchup events in order
	for i := 1; i <= 3; i++ {
		msg = readJSONTimeout(t, conn, 5*time.Second)
		assert.Equal(t, EventTypeTimelineCreated, msg["type"])
		assert.Equal(t, float64(i), msg["sequence_number"])
	}

	// Explicit catchup from the first event's ID — should return only events 2 and 3
	catchupFrom := firstEventID
	catchupMsg, _ := json.Marshal(ClientMessage{
		Action:      "catchup",
		Channel:     env.channel,
		LastEventID: &catchupFrom,
	})
	writeCtx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	require.NoError(t, conn.Write(writeCtx2, websocket.MessageText, catchupMsg))

	for i := 2; i <= 3; i++ {
		msg = readJSONTimeout(t, conn, 5*time.Second)
		assert.Equal(t, float64(i), msg["sequence_number"])
	}

	// No more messages — verify with short timeout
	readCtx, readCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer readCancel()
	_, _, err = conn.Read(readCtx)
	assert.Error(t, err, "should not receive more messages after catchup")
}

func TestIntegration_ResubscribeAfterUnsubscribe_KeepsListen(t *testing.T) {
	// Regression test for the race condition where a rapid unsubscribe/resubscribe
	// cycle (as caused by React StrictMode double-render) would drop the PG LISTEN.
	//
	// The race was:
	//   1. subscribe → LISTEN active
	//   2. unsubscribe → async goroutine: UNLISTEN (deferred)
	//   3. resubscribe → l.Subscribe saw "already listening" → returned early
	//   4. goroutine fired UNLISTEN → PG dropped the LISTEN
	//   5. all subsequent NOTIFY events were silently lost
	//
	// The fix has two parts:
	//   - l.Subscribe always sends LISTEN (no early return; PG handles duplicates)
	//   - the UNLISTEN goroutine re-checks m.channels and skips if resubscribed
	env := setupStreamingTest(t)
	ctx := context.Background()

	conn := env.connectWS(t)
	msg := readJSONTimeout(t, conn, 5*time.Second)
	require.Equal(t, "connection.established", msg["type"])

	// Subscribe
	writeJSON(t, conn, ClientMessage{Action: "subscribe", Channel: env.channel})
	msg = readJSONTimeout(t, conn, 5*time.Second)
	require.Equal(t, "subscription.confirmed", msg["type"])

	require.Eventually(t, func() bool {
		return env.listener.isListening(env.channel)
	}, 2*time.Second, 10*time.Millisecond, "initial LISTEN should propagate")

	// Rapid unsubscribe + resubscribe (mimics React StrictMode cleanup/remount)
	writeJSON(t, conn, ClientMessage{Action: "unsubscribe", Channel: env.channel})
	writeJSON(t, conn, ClientMessage{Action: "subscribe", Channel: env.channel})

	msg = readJSONTimeout(t, conn, 5*time.Second)
	require.Equal(t, "subscription.confirmed", msg["type"])

	// Wait for the UNLISTEN goroutine to settle and verify LISTEN is still active.
	// The goroutine's re-check should see the channel was re-subscribed and skip
	// the UNLISTEN, OR l.Subscribe should have re-issued LISTEN after the UNLISTEN.
	// Either way, the channel must remain listened.
	time.Sleep(200 * time.Millisecond) // Let the async UNLISTEN goroutine run
	require.True(t, env.listener.isListening(env.channel),
		"LISTEN must survive a rapid unsubscribe/resubscribe cycle")

	// Publish an event — it must arrive via pg_notify → listener → WebSocket
	err := env.publisher.PublishTimelineCreated(ctx, env.sessionID, TimelineCreatedPayload{
		BasePayload: BasePayload{
			Type:      EventTypeTimelineCreated,
			SessionID: env.sessionID,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		},
		EventID: "evt-resub-1",
		Content: "should arrive after resubscribe",
	})
	require.NoError(t, err)

	// Drain any catchup events from the resubscribe before checking for the live event
	for {
		msg = readJSONTimeout(t, conn, 5*time.Second)
		if msg["event_id"] == "evt-resub-1" {
			break
		}
	}

	assert.Equal(t, EventTypeTimelineCreated, msg["type"])
	assert.Equal(t, "should arrive after resubscribe", msg["content"])
	assert.Equal(t, env.sessionID, msg["session_id"])
}

func TestIntegration_ListenerGenerationCounter_StaleUnlistenSkipped(t *testing.T) {
	// Tests the generation counter inside NotifyListener directly, bypassing
	// the ConnectionManager. This exercises the exact scenario from code review:
	//
	//   1. Subscribe → LISTEN, gen=1
	//   2. Concurrent Unsubscribe → captures gen=1, enqueues UNLISTEN(gen=1)
	//   3. Subscribe again → gen=2, enqueues LISTEN
	//   4. cmdCh processes: could be LISTEN then UNLISTEN(gen=1)
	//   5. processPendingCmds detects gen mismatch → skips stale UNLISTEN
	//   6. PG stays listened, l.channels stays true
	env := setupStreamingTest(t)
	ctx := context.Background()
	channel := env.channel

	// 1. Initial Subscribe
	require.NoError(t, env.listener.Subscribe(ctx, channel))
	require.True(t, env.listener.isListening(channel))

	// 2. Unsubscribe in a goroutine (simulates the async goroutine in manager)
	unsubDone := make(chan struct{})
	go func() {
		defer close(unsubDone)
		_ = env.listener.Unsubscribe(context.Background(), channel)
	}()

	// 3. Immediately re-Subscribe (may race with the Unsubscribe above)
	require.NoError(t, env.listener.Subscribe(ctx, channel))

	// Wait for the async Unsubscribe to complete
	<-unsubDone

	// Channel must still be listened — the generation counter should have
	// prevented the stale UNLISTEN from taking effect, OR the re-Subscribe's
	// LISTEN should have restored it.
	require.True(t, env.listener.isListening(channel),
		"l.channels must stay true after stale UNLISTEN is skipped")

	// Verify PG is actually listening by publishing an event and receiving it
	conn := env.subscribeAndWait(t)

	err := env.publisher.PublishTimelineCreated(ctx, env.sessionID, TimelineCreatedPayload{
		BasePayload: BasePayload{
			Type:      EventTypeTimelineCreated,
			SessionID: env.sessionID,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		},
		EventID: "evt-gen-1",
		Content: "generation counter test",
	})
	require.NoError(t, err)

	// Drain catchup events, then expect the live event
	for {
		msg := readJSONTimeout(t, conn, 5*time.Second)
		if msg["event_id"] == "evt-gen-1" {
			assert.Equal(t, "generation counter test", msg["content"])
			break
		}
	}
}
