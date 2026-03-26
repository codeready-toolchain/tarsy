package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	testdb "github.com/codeready-toolchain/tarsy/test/database"
	"github.com/codeready-toolchain/tarsy/test/e2e/testdata/configs"
)

// ────────────────────────────────────────────────────────────
// Multi-replica test — verifies cross-replica WebSocket event delivery
// via PostgreSQL NOTIFY/LISTEN.
//
// Two TARSy replicas share the same PostgreSQL schema:
//   - Replica 1: has workers, claims and runs sessions.
//   - Replica 2: zero workers (API/WS only), never claims sessions.
//
// A session is created via replica 1. A WebSocket client connected to
// replica 2 subscribes to that session's channel. The test verifies
// that WS events (session status, stage status, timeline events) are
// delivered to replica 2 via PostgreSQL NOTIFY/LISTEN — the exact
// production code path for multi-pod deployments.
// ────────────────────────────────────────────────────────────

func TestE2E_MultiReplica(t *testing.T) {
	// ═══════════════════════════════════════════════════════
	// Shared database (one schema, two independent clients)
	// ═══════════════════════════════════════════════════════

	sharedDB := testdb.NewSharedTestDB(t)

	// ═══════════════════════════════════════════════════════
	// LLM mock (shared — only the claiming replica uses it)
	// ═══════════════════════════════════════════════════════

	llm := NewScriptedLLMClient()

	// SimpleAgent — single iteration, simple response.
	llm.AddRouted("SimpleAgent", LLMScriptEntry{
		Text: "Analysis complete: system is healthy.",
	})

	// Executive summary.
	llm.AddSequential(LLMScriptEntry{
		Text: "Executive summary: all clear.",
	})

	// ═══════════════════════════════════════════════════════
	// Boot two replicas
	// Each gets its own config because NewTestApp mutates cfg.Queue.
	// ═══════════════════════════════════════════════════════

	// Replica 1: worker-enabled, claims and executes sessions.
	app1 := NewTestApp(t,
		WithConfig(configs.Load(t, "multi-replica")),
		WithDBClient(sharedDB.NewClient(t)),
		WithLLMClient(llm),
		WithPodID("replica-1"),
	)

	// Replica 2: zero workers (API/WS only). Receives events via
	// PostgreSQL NOTIFY/LISTEN but never claims sessions.
	app2 := NewTestApp(t,
		WithConfig(configs.Load(t, "multi-replica")),
		WithDBClient(sharedDB.NewClient(t)),
		WithLLMClient(llm),
		WithPodID("replica-2"),
		WithWorkerCount(0),
	)

	// ═══════════════════════════════════════════════════════
	// Connect WS to replica 2 BEFORE creating the session
	// ═══════════════════════════════════════════════════════

	ctx := context.Background()
	ws, err := WSConnect(ctx, app2.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	// ═══════════════════════════════════════════════════════
	// Create session via replica 1
	// ═══════════════════════════════════════════════════════

	resp := app1.SubmitAlert(t, "test-multi-replica", "Multi-replica cross-pod event delivery test")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)

	// Subscribe on replica 2's WS to the session created via replica 1.
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	// ═══════════════════════════════════════════════════════
	// Wait for session to complete (poll via replica 1)
	// ═══════════════════════════════════════════════════════

	app1.WaitForSessionStatus(t, sessionID, "completed")

	// ═══════════════════════════════════════════════════════
	// Assert: WS events received on replica 2 (cross-replica)
	// ═══════════════════════════════════════════════════════

	// Wait for each expected cross-replica event type individually, rather
	// than asserting their presence after a single WaitForEvent. Events may
	// arrive via PG NOTIFY at slightly different times, so waiting only for
	// session.status and then immediately asserting can miss events still
	// in flight on the WS connection.
	wsTimeout := 5 * time.Second

	ws.WaitForEvent(t, func(e WSEvent) bool {
		return e.Type == "session.status" && e.Parsed["status"] == "completed"
	}, wsTimeout, "replica 2 should receive session.status completed via WS")

	ws.WaitForEvent(t, func(e WSEvent) bool {
		return e.Type == "stage.status"
	}, wsTimeout, "replica 2 should receive stage.status events via WS")

	ws.WaitForEvent(t, func(e WSEvent) bool {
		return e.Type == "timeline_event.created"
	}, wsTimeout, "replica 2 should receive timeline_event.created events via WS")

	// ═══════════════════════════════════════════════════════
	// Assert: REST API cross-replica (GET session via replica 2)
	// ═══════════════════════════════════════════════════════

	session := app2.GetSession(t, sessionID)
	assert.Equal(t, "completed", session["status"],
		"replica 2 should see the completed session via REST API")

	// ═══════════════════════════════════════════════════════
	// Assert: Timeline visible on replica 2
	// ═══════════════════════════════════════════════════════

	timeline := app2.GetTimeline(t, sessionID)
	assert.NotEmpty(t, timeline,
		"replica 2 should see timeline events via REST API")

	// ── Total LLM call count ──
	// 1 session × (1 SimpleAgent + 1 executive summary) = 2
	assert.Equal(t, 2, llm.CallCount())
}
