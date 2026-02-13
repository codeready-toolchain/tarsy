# Phase 7: Dashboard — Technical Design

## Old TARSy Reference

See the [Plan doc](phase7-dashboard-plan.md#old-tarsy-reference) for a full path table. Key paths for quick access:

- **Old dashboard source**: `/home/igels/Projects/AI/tarsy-bot/dashboard/src/`
- **Old backend controllers**: `/home/igels/Projects/AI/tarsy-bot/backend/tarsy/controllers/`
- **Old event system**: `/home/igels/Projects/AI/tarsy-bot/backend/tarsy/services/events/`

---

## Technology Stack

Preserved from old dashboard — no framework migration:

| Area | Choice | Notes |
|------|--------|-------|
| Framework | React 19 + TypeScript | Same as old |
| Build | Vite 7 | Same as old |
| UI library | MUI 7 (Material UI) | Same as old |
| Routing | React Router DOM 7 | Same as old |
| HTTP client | Axios | Same as old |
| WebSocket | Native WebSocket (custom service) | Adapted for new protocol |
| Markdown | react-markdown + react-syntax-highlighter | Same as old |
| JSON display | react-json-view-lite | Same as old |
| Dates | date-fns | Same as old |
| Virtualization | react-window + react-window-infinite-loader | Same as old (if needed) |

---

## Project Structure

```
web/dashboard/
├── src/
│   ├── App.tsx                    # Root: routing, theme provider, context providers
│   ├── main.tsx                   # Entry point
│   ├── config/
│   │   └── env.ts                 # API/WS URL configuration
│   ├── types/
│   │   ├── api.ts                 # API response types
│   │   ├── events.ts              # WebSocket event types
│   │   └── index.ts               # Re-exports
│   ├── services/
│   │   ├── api.ts                 # REST API client (all endpoints)
│   │   ├── auth.ts                # Auth service (oauth2-proxy userinfo, 401 handling)
│   │   └── websocket.ts           # WebSocket service (new protocol)
│   ├── contexts/
│   │   ├── AuthContext.tsx         # Auth state (oauth2-proxy, graceful degradation)
│   │   └── SessionContext.tsx      # Session state management
│   ├── hooks/
│   │   ├── useAutoScroll.ts       # Smart auto-scroll
│   │   ├── useChatState.ts        # Chat state management
│   │   ├── useVersionMonitor.ts   # Backend version polling + mismatch detection
│   │   └── useWebSocket.ts        # WebSocket subscription hook
│   ├── theme/
│   │   └── index.ts               # MUI theme definition
│   ├── utils/
│   │   ├── timelineParser.ts      # Timeline events → UI flow items
│   │   ├── statusMapping.ts       # Status → display attributes
│   │   ├── markdownComponents.tsx  # Custom markdown renderers
│   │   ├── filterPersistence.ts   # localStorage persistence
│   │   └── timestamp.ts           # Date formatting
│   ├── components/
│   │   ├── layout/
│   │   │   ├── SharedHeader.tsx
│   │   │   ├── SystemWarningBanner.tsx
│   │   │   ├── VersionUpdateBanner.tsx
│   │   │   └── VersionFooter.tsx
│   │   ├── dashboard/
│   │   │   ├── DashboardView.tsx
│   │   │   ├── DashboardLayout.tsx
│   │   │   ├── ActiveAlertsPanel.tsx
│   │   │   ├── QueuedAlertsSection.tsx
│   │   │   ├── HistoricalAlertsList.tsx
│   │   │   ├── AlertListItem.tsx
│   │   │   ├── ActiveAlertCard.tsx
│   │   │   ├── ChainProgressCard.tsx
│   │   │   ├── FilterPanel.tsx
│   │   │   ├── FilterBar.tsx
│   │   │   └── StatusFilter.tsx
│   │   ├── session/
│   │   │   ├── SessionDetailPage.tsx
│   │   │   ├── SessionHeader.tsx
│   │   │   ├── OriginalAlertCard.tsx
│   │   │   ├── FinalAnalysisCard.tsx
│   │   │   └── ExecutiveSummaryCard.tsx
│   │   ├── timeline/
│   │   │   ├── ConversationTimeline.tsx
│   │   │   ├── TimelineItem.tsx
│   │   │   ├── ThinkingItem.tsx
│   │   │   ├── ResponseItem.tsx
│   │   │   ├── ToolCallItem.tsx
│   │   │   ├── ToolSummaryItem.tsx
│   │   │   ├── NativeToolItem.tsx
│   │   │   ├── ErrorItem.tsx
│   │   │   ├── StageSeparator.tsx
│   │   │   └── ParallelStageTabs.tsx
│   │   ├── streaming/
│   │   │   ├── StreamingContentRenderer.tsx
│   │   │   ├── TypewriterText.tsx
│   │   │   └── TypingIndicator.tsx
│   │   ├── chat/
│   │   │   ├── ChatPanel.tsx
│   │   │   ├── ChatInput.tsx
│   │   │   ├── ChatMessageList.tsx
│   │   │   ├── ChatUserMessageCard.tsx
│   │   │   └── ChatAssistantMessageCard.tsx
│   │   ├── trace/
│   │   │   ├── TraceView.tsx
│   │   │   ├── TraceTimeline.tsx
│   │   │   ├── StageAccordion.tsx
│   │   │   ├── ExecutionGroup.tsx
│   │   │   ├── InteractionCard.tsx
│   │   │   ├── LLMInteractionDetail.tsx
│   │   │   └── MCPInteractionDetail.tsx
│   │   ├── alerts/
│   │   │   ├── ManualAlertSubmission.tsx
│   │   │   ├── ManualAlertForm.tsx
│   │   │   └── MCPSelection.tsx
│   │   └── shared/
│   │       ├── StatusBadge.tsx
│   │       ├── MarkdownRenderer.tsx
│   │       ├── JsonDisplay.tsx
│   │       └── CopyButton.tsx
│   └── constants/
│       ├── eventTypes.ts
│       ├── statusConstants.ts
│       └── routes.ts
├── public/
├── index.html
├── package.json
├── tsconfig.json
└── vite.config.ts
```

---

## Backend API Extensions (Phase 7.0)

### Existing Endpoints (no changes needed)

These already exist and are used by the dashboard as-is:

- `GET /api/v1/sessions/:id/timeline` — Timeline events for conversation view
- `GET /api/v1/sessions/:id/trace` — Trace list (stages → executions → interactions)
- `GET /api/v1/sessions/:id/trace/llm/:interaction_id` — LLM interaction detail
- `GET /api/v1/sessions/:id/trace/mcp/:interaction_id` — MCP interaction detail
- `POST /api/v1/alerts` — Submit alert
- `POST /api/v1/sessions/:id/cancel` — Cancel session
- `POST /api/v1/sessions/:id/chat/messages` — Send chat message

### Moved Endpoints

#### WebSocket: `/ws` → `/api/v1/ws`

Move WebSocket endpoint from `/ws` to `/api/v1/ws`. Rationale: all sensitive endpoints under `/api/*` for a single oauth2-proxy auth rule; protocol versioning alignment with REST API.

### Enriched Endpoints

#### GET /health — Health Check (enrichment)

Exists today. Needs `version` field added (build-time git tag or hash) for dashboard version monitoring and footer display.

#### GET /api/v1/sessions/:id — Enriched Session Detail

Exists today but returns raw `ent.AlertSession` JSON. Needs computed fields added (see section 3 below).

### New Endpoints

#### 1. GET /api/v1/sessions — List Sessions (NEW)

**Query parameters**:

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `status` | string (comma-separated) | all | Filter by status(es) |
| `alert_type` | string | all | Filter by alert type |
| `chain_id` | string | all | Filter by chain ID |
| `search` | string | — | ILIKE search on alert_data, final_analysis |
| `start_date` | RFC3339 | — | Created after |
| `end_date` | RFC3339 | — | Created before |
| `page` | int | 1 | Page number (1-based) |
| `page_size` | int | 25 | Items per page (max 100) |
| `sort_by` | string | `created_at` | Sort field |
| `sort_order` | string | `desc` | `asc` or `desc` |

**Response** (200):

```json
{
  "sessions": [
    {
      "id": "uuid",
      "alert_type": "string",
      "chain_id": "string",
      "status": "completed",
      "author": "string",
      "created_at": "RFC3339",
      "started_at": "RFC3339",
      "completed_at": "RFC3339",
      "duration_ms": 45000,
      "error_message": null,
      "executive_summary": "Concise summary of the investigation outcome",
      "llm_interaction_count": 5,
      "mcp_interaction_count": 3,
      "input_tokens": 12000,
      "output_tokens": 3000,
      "total_tokens": 15000,
      "total_stages": 4,
      "completed_stages": 4,
      "has_parallel_stages": true,
      "chat_message_count": 2,
      "current_stage_index": null,
      "current_stage_id": null
    }
  ],
  "pagination": {
    "page": 1,
    "page_size": 25,
    "total_pages": 10,
    "total_items": 245
  }
}
```

**Implementation**: New handler in `pkg/api/handler_session.go`. New `SessionService.ListSessions()` method with query builder. Aggregation via Ent edges (count interactions, sum tokens via SQL aggregate). Search via `ILIKE` on `alert_data` and `final_analysis` fields (can be upgraded to full-text search with GIN indexes later without API changes).

Sort field mapping:

| `sort_by` value | DB column |
|----------------|-----------|
| `created_at` | `alert_session.created_at` |
| `status` | `alert_session.status` |
| `alert_type` | `alert_session.alert_type` |
| `author` | `alert_session.author` |
| `duration` | computed: `completed_at - started_at` |

---

#### 2. GET /api/v1/sessions/active — Active Sessions

Returns sessions that are actively being processed or queued.

**Response** (200):

```json
{
  "active": [
    {
      "id": "uuid",
      "alert_type": "string",
      "chain_id": "string",
      "status": "in_progress",
      "author": "string",
      "created_at": "RFC3339",
      "started_at": "RFC3339",
      "current_stage_index": 2,
      "current_stage_id": "uuid",
      "total_stages": 4
    }
  ],
  "queued": [
    {
      "id": "uuid",
      "alert_type": "string",
      "chain_id": "string",
      "status": "pending",
      "author": "string",
      "created_at": "RFC3339",
      "queue_position": 3
    }
  ]
}
```

**Implementation**: Two queries: `status IN (in_progress, cancelling)` and `status = pending ORDER BY created_at`. Queue position computed from row ordering.

---

#### 3. GET /api/v1/sessions/:id — Enriched Session Detail (EXISTING — enrichment)

Extend existing handler to return computed fields. The raw `ent.AlertSession` fields remain; additional computed fields are added.

**Additional response fields** (beyond existing ent JSON):

```json
{
  "...existing fields...",
  "duration_ms": 45000,
  "chat_enabled": true,
  "chat_id": "uuid",
  "chat_message_count": 2,
  "total_stages": 4,
  "completed_stages": 4,
  "failed_stages": 0,
  "has_parallel_stages": true,
  "input_tokens": 12000,
  "output_tokens": 3000,
  "total_tokens": 15000,
  "llm_interaction_count": 5,
  "mcp_interaction_count": 3,
  "stages": [
    {
      "id": "uuid",
      "stage_name": "Investigation",
      "stage_index": 1,
      "status": "completed",
      "parallel_type": null,
      "expected_agent_count": 1,
      "started_at": "RFC3339",
      "completed_at": "RFC3339"
    }
  ]
}
```

**Implementation**: Replace raw `c.JSON(session)` with a `SessionDetailResponse` DTO. Load edges: stages (with status), chat (with message count), interactions (aggregate tokens).

---

#### 4. GET /api/v1/sessions/:id/summary — Session Statistics

Lightweight stats endpoint for the session header.

**Response** (200):

```json
{
  "session_id": "uuid",
  "total_interactions": 8,
  "llm_interactions": 5,
  "mcp_interactions": 3,
  "input_tokens": 12000,
  "output_tokens": 3000,
  "total_tokens": 15000,
  "total_duration_ms": 45000,
  "chain_statistics": {
    "total_stages": 4,
    "completed_stages": 4,
    "failed_stages": 0,
    "current_stage_index": null
  }
}
```

---

#### 5. GET /api/v1/sessions/filter-options — Filter Options

Returns distinct values currently in use.

**Response** (200):

```json
{
  "alert_types": ["pod-crash", "node-pressure", "deployment-failed"],
  "chain_ids": ["standard-investigation", "quick-check"],
  "statuses": ["completed", "failed", "cancelled", "timed_out", "in_progress", "pending"]
}
```

**Implementation**: `SELECT DISTINCT alert_type, chain_id FROM alert_sessions WHERE deleted_at IS NULL`.

---

#### 6. System Endpoints

**GET /api/v1/system/warnings**:

```json
{
  "warnings": [
    {
      "id": "uuid",
      "category": "mcp_health",
      "message": "MCP server 'kubernetes' is unhealthy",
      "details": "connection refused",
      "server_id": "kubernetes",
      "created_at": "RFC3339"
    }
  ]
}
```

**GET /api/v1/system/mcp-servers**:

```json
{
  "servers": [
    {
      "id": "kubernetes",
      "healthy": true,
      "last_check": "RFC3339",
      "tool_count": 12,
      "tools": ["get_pods", "get_logs", "..."],
      "error": null
    }
  ]
}
```

**GET /api/v1/system/default-tools**:

```json
{
  "native_tools": {
    "google_search": true,
    "code_execution": true,
    "url_context": false
  }
}
```

**GET /api/v1/alert-types**:

```json
{
  "alert_types": [
    {
      "type": "pod-crash",
      "chain_id": "standard-investigation",
      "description": "Pod crash investigation"
    }
  ],
  "default_chain_id": "standard-investigation"
}
```

---

#### 7. Progress Status Events

Two levels of transient progress events (NOTIFY only, no DB persistence). Needed because sessions can have multiple parallel agent executions — a single progress event can't serve both the session list and the per-agent detail view.

**7a. Session-level progress** — published to `sessions` channel (global):

Used by the active alerts panel to show current stage and high-level status without REST re-fetch.

```json
{
  "type": "session.progress",
  "session_id": "uuid",
  "current_stage_name": "Parallel Analysis",
  "current_stage_index": 2,
  "total_stages": 4,
  "active_executions": 3,
  "status_text": "Running 3 agents in parallel...",
  "timestamp": "RFC3339Nano"
}
```

Published on: stage transitions, synthesis/finalization start, periodically during long stages.

**7b. Execution-level progress** — published to `session:{id}` channel:

Used by the session detail page to show per-agent progress in the stage display and parallel agent tabs.

```json
{
  "type": "execution.progress",
  "session_id": "uuid",
  "stage_id": "uuid",
  "execution_id": "uuid",
  "phase": "gathering_info",
  "message": "Gathering info...",
  "timestamp": "RFC3339Nano"
}
```

**ProgressPhase enum** (for execution-level events):

| Phase | Published From | Trigger |
|-------|---------------|---------|
| `investigating` | Controller iteration start | Each iteration loop start |
| `gathering_info` | `executeToolCall()` | Before MCP tool execution |
| `distilling` | `maybeSummarize()` | Before summarization LLM call |
| `concluding` | `forceConclusion()` | At max iterations |
| `synthesizing` | `executeSynthesisStage()` | Synthesis stage start |
| `finalizing` | `generateExecutiveSummary()` | Executive summary generation |

**Implementation**:
- `PublishSessionProgress(ctx, sessionID, stageName, stageIndex, totalStages, activeExecs, statusText)` → publishes to `sessions` channel via `PublishTransient()`
- `PublishExecutionProgress(ctx, sessionID, stageID, execID, phase, message)` → publishes to `session:{id}` channel via `PublishTransient()`
- Retrofit calls into existing code paths (controllers, tool executor, summarizer, parallel executor)

### Interaction Created Event

**Event type**: `interaction.created`
**Channel**: `session:{id}`
**Purpose**: Lightweight notification when an LLM or MCP interaction record is saved to the database. Used by the trace view for live updates via event-notification + REST re-fetch pattern.

**Payload**:
```json
{
  "event": "interaction.created",
  "session_id": "sess-123",
  "stage_id": "stage-456",
  "execution_id": "exec-789",
  "interaction_id": "int-abc",
  "interaction_type": "llm"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `session_id` | string | Session ID |
| `stage_id` | string | Stage the interaction belongs to |
| `execution_id` | string | Execution the interaction belongs to |
| `interaction_id` | string | Interaction ID (LLM or MCP) |
| `interaction_type` | `"llm"` \| `"mcp"` | Type of interaction |

**Note**: Interactions are saved as complete records (no create-then-update lifecycle). The event fires once, after the LLM/MCP call completes and the full record is persisted. This is a persisted event (not transient) since it represents a DB record creation.

**Implementation**:
- `PublishInteractionCreated(ctx, sessionID, stageID, execID, interactionID, interactionType)` → publishes to `session:{id}` channel via `Publish()`
- Add publish call in `InteractionService.CreateLLMInteraction()` and `InteractionService.CreateMCPInteraction()` (or in the calling code: `recordLLMInteraction`, `recordMCPInteraction`)

---

## WebSocket Protocol Adaptation

### Event Type Mapping (Old → New)

| Old Event | New Event | Adaptation |
|-----------|-----------|------------|
| `session.created` | `session.status` (status=pending) | Map from status field |
| `session.started` | `session.status` (status=in_progress) | Map from status field |
| `session.completed` | `session.status` (status=completed) | Map from status field |
| `session.failed` | `session.status` (status=failed) | Map from status field |
| `session.cancelled` | `session.status` (status=cancelled) | Map from status field |
| `session.timed_out` | `session.status` (status=timed_out) | Map from status field |
| `session.paused` | N/A | Feature removed (no paused state) |
| `session.resumed` | N/A | Feature removed |
| `session.progress_update` | `session.progress` + `execution.progress` | Split: session-level (global channel) + execution-level (session channel) |
| `stage.started` | `stage.status` (status=started) | Map from status field |
| `stage.completed` | `stage.status` (status=completed) | Map from status field |
| `stage.failed` | `stage.status` (status=failed) | Map from status field |
| N/A | `stage.status` (status=timed_out) | New in new TARSy — stage-level timeout |
| N/A | `stage.status` (status=cancelled) | New in new TARSy — stage-level cancellation |
| `llm.interaction` | `timeline_event.created` | Different model: timeline events carry content |
| `mcp.tool_call.started` | `timeline_event.created` (llm_tool_call, streaming) | Lifecycle model |
| `mcp.tool_call` | `timeline_event.completed` (llm_tool_call) | Lifecycle model |
| `llm.stream.chunk` | `stream.chunk` | Simpler: `{delta}` vs `{chunk, stream_type}` |
| `chat.created` | `chat.created` | Same (fired once when chat is first created) |
| `chat.user_message` | N/A | Removed — user messages appear as `timeline_event.created` with `event_type: "user_question"` |
| N/A | `timeline_event.created` (status=cancelled) | New — timeline events can be cancelled |
| N/A | `timeline_event.created` (status=timed_out) | New — timeline events can time out |
| N/A | `timeline_event.completed` (status=cancelled/timed_out) | New — streaming events can terminate with cancel/timeout |
| N/A | `interaction.created` | New — lightweight notification when LLM/MCP interaction is saved. Used by trace view for live updates (event-notification → REST re-fetch) |

### Key Protocol Differences

**1. Unified status events**: Old protocol dispatched many event types (`session.created`, `session.started`, etc.). New protocol uses a single `session.status` event with a `status` field. The WebSocket service maps this internally:

```typescript
// New: event handler receives unified status
ws.on('session.status', (data) => {
  // data.status = 'completed' | 'failed' | 'in_progress' | ...
  updateSessionStatus(data.session_id, data.status);
});
```

**2. Timeline-centric model**: Old protocol sent interaction-level events (`llm.interaction`, `mcp.tool_call`). New protocol is timeline-centric — `timeline_event.created` and `timeline_event.completed` carry the `event_type` field that describes what happened. The dashboard reads `event_type` to determine rendering:

```typescript
ws.on('timeline_event.created', (data) => {
  switch (data.event_type) {
    case 'llm_thinking': renderThinking(data); break;
    case 'llm_response': renderResponse(data); break;
    case 'llm_tool_call': renderToolCall(data); break;
    case 'mcp_tool_summary': renderSummary(data); break;
    // ...
  }
});
```

**3. Streaming model**: Old used `llm.stream.chunk` with `stream_type` to indicate what's being streamed (thought, final_answer, summarization). New uses `stream.chunk` with just `event_id` + `delta`. The stream type is implicit from the timeline event that started streaming (identified by `event_id`):

```typescript
// Track active streaming events
const streamingEvents = new Map<string, TimelineEvent>();

ws.on('timeline_event.created', (data) => {
  if (data.status === 'streaming') {
    streamingEvents.set(data.event_id, data);
  }
});

ws.on('stream.chunk', (data) => {
  const event = streamingEvents.get(data.event_id);
  if (event) {
    appendDelta(event.event_id, data.delta);
  }
});

ws.on('timeline_event.completed', (data) => {
  streamingEvents.delete(data.event_id);
  finalizeEvent(data.event_id, data.content);
});
```

**4. WebSocket URL**: Move from current `/ws` to `/api/v1/ws` (matching old TARSy). Rationale: single oauth2-proxy rule (`/api/*`) covers all sensitive endpoints (REST + WS); protocol versioning; clean separation from static files. Backend change: move route registration in `server.go` (Phase 7.0). Configure in `env.ts`.

**5. Catchup**: Both support auto-catchup on subscribe + explicit catchup with `last_event_id`. New protocol adds `catchup.overflow` signal (limit 200 events, signal full REST reload if more). Dashboard must handle overflow by refreshing from REST.

---

## Frontend Architecture

### Timeline Event → UI Flow Item Mapping

The `timelineParser.ts` module converts timeline events into renderable flow items:

```typescript
interface FlowItem {
  id: string;
  type: 'thinking' | 'response' | 'tool_call' | 'tool_summary' | 'error' |
        'final_analysis' | 'executive_summary' | 'user_question' |
        'code_execution' | 'search_result' | 'url_context' | 'stage_separator';
  stageId?: string;
  executionId?: string;
  content: string;
  metadata?: Record<string, any>;
  status: 'streaming' | 'completed' | 'failed';
  timestamp: string;
}

function parseTimelineToFlow(events: TimelineEvent[]): FlowItem[] {
  // Group by stage, handle parallel stages
  // Convert each event to appropriate FlowItem
  // Insert stage separators at stage boundaries
  // Handle tool_call lifecycle (merge call + result into one item)
}
```

### Streaming Content Rendering

Old dashboard's `StreamingContentRenderer` handles multiple stream types. New dashboard simplifies this since stream type is determined by the parent timeline event's `event_type`:

```typescript
// Determine rendering style from timeline event type
function getStreamRenderer(eventType: string) {
  switch (eventType) {
    case 'llm_thinking': return ThinkingRenderer;   // Collapsible, muted
    case 'llm_response': return ResponseRenderer;    // Markdown, typewriter
    case 'mcp_tool_summary': return SummaryRenderer; // Compact markdown
    case 'final_analysis': return AnalysisRenderer;  // Full markdown
    default: return DefaultRenderer;
  }
}
```

### Chat Integration

Chat messages are unified with the session timeline. No separate chat history API needed.

**Data flow**:
1. User sends message → `POST /sessions/:id/chat/messages` → 202 Accepted (returns `chat_id`, `message_id`, `stage_id`)
2. Dashboard shows user message **optimistically** from the POST response content (no separate WS event — `chat.user_message` doesn't exist in new TARSy)
3. Dashboard receives `chat.created` (if first message only) → update chat state
4. Dashboard receives `stage.status` (started) → show "processing" indicator
5. Dashboard receives `timeline_event.created` (type=`user_question`) → confirm user message in timeline
6. Dashboard receives `timeline_event.created` (status=streaming) → start streaming assistant response
7. Dashboard receives `stream.chunk` events → append deltas
8. Dashboard receives `timeline_event.completed` → finalize response
9. Dashboard receives `stage.status` (completed) → clear processing indicator

**Cancel**: Call `POST /sessions/:id/cancel` (same endpoint as session cancel — works for both).

### Parallel Stage Rendering

Stages with multiple agents (parallel_type = multi_agent or replica) are rendered with tabs:

```
┌─ Stage: Investigation ─── [Agent-1] [Agent-2] [Agent-3] ──┐
│  [Agent-1 tab selected]                                   │
│  - Thinking: "Let me check the pod status..."             │
│  - Tool Call: kubernetes.get_pods                         │
│  - Response: "The pod is in CrashLoopBackOff..."          │
└───────────────────────────────────────────────────────────┘
```

Timeline events carry `execution_id` which allows grouping by agent. Stage metadata (from enriched session detail) provides the parallel type and agent names.

### Trace View Data Flow

Available for all sessions (active and terminated). Uses event-notification + REST re-fetch pattern for live updates.

```
Initial load:
1. Load session detail:    GET /sessions/:id
2. Load trace list:        GET /sessions/:id/trace → TraceListResponse
3. Render stage hierarchy: stages → executions → interaction cards

Live updates (active sessions):
4. Subscribe to session:{id} channel (already subscribed from page load)
5. On stage.status event        → re-fetch GET /sessions/:id/trace
6. On interaction.created event → re-fetch GET /sessions/:id/trace
7. Re-render stage hierarchy with updated data

Interaction detail (on demand):
8. On interaction click:   GET /sessions/:id/trace/llm/:id  (or .../mcp/:id)
9. Show interaction detail in expanded card or modal
   (always returns complete data — interactions are saved as complete records)
```

**Design rationale**: Events are lightweight "something changed" notifications. REST endpoints are the source of truth — no complex client-side state stitching needed. For terminated sessions, no events arrive and the view is purely static (same REST endpoints, no re-fetches).

---

## Session Detail Response DTO

New response type replacing raw `ent.AlertSession` JSON:

```go
// pkg/api/responses.go

type SessionDetailResponse struct {
    // Core fields (from AlertSession)
    ID               string  `json:"id"`
    AlertData        string  `json:"alert_data"`
    AlertType        string  `json:"alert_type"`
    Status           string  `json:"status"`
    ChainID          string  `json:"chain_id"`
    Author           *string `json:"author"`
    ErrorMessage     *string `json:"error_message"`
    FinalAnalysis    *string `json:"final_analysis"`
    ExecutiveSummary *string `json:"executive_summary"`
    RunbookURL       *string `json:"runbook_url"`

    // Timestamps
    CreatedAt   time.Time  `json:"created_at"`
    StartedAt   *time.Time `json:"started_at"`
    CompletedAt *time.Time `json:"completed_at"`

    // Computed fields
    DurationMs         *int64 `json:"duration_ms"`
    ChatEnabled        bool   `json:"chat_enabled"`
    ChatID             *string `json:"chat_id"`
    ChatMessageCount   int    `json:"chat_message_count"`
    TotalStages        int    `json:"total_stages"`
    CompletedStages    int    `json:"completed_stages"`
    FailedStages       int    `json:"failed_stages"`
    HasParallelStages  bool   `json:"has_parallel_stages"`
    InputTokens        int64  `json:"input_tokens"`
    OutputTokens       int64  `json:"output_tokens"`
    TotalTokens        int64  `json:"total_tokens"`
    LLMInteractionCount int   `json:"llm_interaction_count"`
    MCPInteractionCount int   `json:"mcp_interaction_count"`
    CurrentStageIndex  *int   `json:"current_stage_index"`
    CurrentStageID     *string `json:"current_stage_id"`

    // Stage list
    Stages []StageOverview `json:"stages"`
}

type StageOverview struct {
    ID                 string  `json:"id"`
    StageName          string  `json:"stage_name"`
    StageIndex         int     `json:"stage_index"`
    Status             string  `json:"status"`
    ParallelType       *string `json:"parallel_type"`
    ExpectedAgentCount int     `json:"expected_agent_count"`
    StartedAt          *time.Time `json:"started_at"`
    CompletedAt        *time.Time `json:"completed_at"`
}
```

---

## Session List Response DTO

```go
// pkg/api/responses.go

type SessionListResponse struct {
    Sessions   []SessionListItem `json:"sessions"`
    Pagination PaginationInfo    `json:"pagination"`
}

type SessionListItem struct {
    ID                  string  `json:"id"`
    AlertType           string  `json:"alert_type"`
    ChainID             string  `json:"chain_id"`
    Status              string  `json:"status"`
    Author              *string `json:"author"`
    CreatedAt           time.Time  `json:"created_at"`
    StartedAt           *time.Time `json:"started_at"`
    CompletedAt         *time.Time `json:"completed_at"`
    DurationMs          *int64  `json:"duration_ms"`
    ErrorMessage        *string `json:"error_message"`
    ExecutiveSummary      *string `json:"executive_summary"`
    LLMInteractionCount int     `json:"llm_interaction_count"`
    MCPInteractionCount int     `json:"mcp_interaction_count"`
    InputTokens         int64   `json:"input_tokens"`
    OutputTokens        int64   `json:"output_tokens"`
    TotalTokens         int64   `json:"total_tokens"`
    TotalStages         int     `json:"total_stages"`
    CompletedStages     int     `json:"completed_stages"`
    HasParallelStages   bool    `json:"has_parallel_stages"`
    ChatMessageCount    int     `json:"chat_message_count"`
    CurrentStageIndex   *int    `json:"current_stage_index"`
    CurrentStageID      *string `json:"current_stage_id"`
}

type PaginationInfo struct {
    Page       int `json:"page"`
    PageSize   int `json:"page_size"`
    TotalPages int `json:"total_pages"`
    TotalItems int `json:"total_items"`
}
```

**Implementation strategy for aggregated stats**: Use SQL subqueries or Ent edge aggregation to compute stats in-database rather than loading all interactions into Go memory:

```sql
SELECT s.*,
  (SELECT COUNT(*) FROM llm_interactions WHERE session_id = s.id) AS llm_count,
  (SELECT COALESCE(SUM(total_tokens), 0) FROM llm_interactions WHERE session_id = s.id) AS total_tokens,
  (SELECT COUNT(*) FROM stages WHERE session_id = s.id) AS total_stages,
  ...
FROM alert_sessions s
WHERE s.deleted_at IS NULL
ORDER BY s.created_at DESC
LIMIT 25 OFFSET 0;
```

Or via Ent:

```go
sessions, err := client.AlertSession.Query().
    Where(alertsession.DeletedAtIsNil()).
    WithStages(func(q *ent.StageQuery) { q.Select(stage.FieldStatus) }).
    WithChats(func(q *ent.ChatQuery) { q.WithMessages() }).
    Order(ent.Desc(alertsession.FieldCreatedAt)).
    Limit(pageSize).Offset(offset).
    All(ctx)
```

---

## Go Static File Serving

### Development Mode

Vite dev server with proxy:

```typescript
// web/dashboard/vite.config.ts
export default defineConfig({
  server: {
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        // Covers both REST (/api/v1/*) and WebSocket (/api/v1/ws)
        ws: true,
      },
      '/health': 'http://localhost:8080',
    },
  },
});
```

### Production Mode

Go serves the built dashboard from disk. Single container — oauth2-proxy sits in front for auth.

```go
// cmd/tarsy/main.go
if dashboardDir != "" {
    e.Static("/", dashboardDir)
    // SPA fallback: serve index.html for all non-API routes
    e.GET("/*", func(c echo.Context) error {
        return c.File(filepath.Join(dashboardDir, "index.html"))
    })
}
```

**Production deployment**: `oauth2-proxy → Go (serves static + /api/* + /ws)` — single upstream, no CORS, no Nginx.

---

## Per-Sub-Phase Implementation Notes

### Phase 7.1 (Foundation) — Key Decisions

- **API service**: Type all endpoint methods. Return typed responses. Handle errors uniformly. 401 → redirect to `/oauth2/sign_in` if auth configured.
- **WebSocket service**: New event type handling. Map `session.status` → per-status callbacks. Track streaming events by `event_id`.
- **Theme**: Copy MUI theme from old dashboard exactly. Primary: `#1976d2`, Roboto, light mode.
- **Routing**: Use `createBrowserRouter` (React Router 7). SPA fallback handled by Go.
- **Auth**: `AuthContext` with full UI — login/logout buttons, user menu from `X-Forwarded-User`. `services/auth.ts` handles oauth2-proxy userinfo check and 401 redirects. Graceful degradation when oauth2-proxy is not configured (no auth elements shown). Ready for Phase 9 backend wiring.
- **Layout component shells**: `VersionFooter`, `VersionUpdateBanner`, `SystemWarningBanner` are created as component shells in 7.1 (rendered in layout). Wired to real data (health polling, system warnings polling) in Phase 7.7.
- **Version monitoring** (wired in 7.7): `useVersionMonitor` hook polls health endpoint, compares `version` to build-time UI version. `VersionUpdateBanner` on mismatch. `VersionFooter` shows both UI and backend versions.

### Phase 7.2 (Session List) — Data Flow

1. On mount: `GET /sessions/filter-options` → populate filters
2. On mount: `GET /sessions?page=1&page_size=25&sort_by=created_at&sort_order=desc` → populate list
3. On mount: `GET /sessions/active` → populate active + queued panels
4. Subscribe to `sessions` channel:
   - On `session.status` → re-fetch `GET /sessions/active` for active/queued panels; if terminal → also re-fetch historical list
   - On `session.progress` → update active session card in-place (stage name, stage index, status text) without REST re-fetch
5. On filter/sort/page change: re-fetch `GET /sessions` with new params
6. Persist filter state in localStorage

### Phase 7.3 (Session Detail) — Event Flow

1. On mount: parallel fetch via `Promise.all([getSession(id), getTimeline(id)])` → session detail + timeline events
2. Parse timeline events into flow items (group by stage, handle parallel)
3. Subscribe to `session:{id}` channel
4. On `timeline_event.created`:
   - status=streaming → add placeholder flow item, start streaming
   - status=completed → add completed flow item
5. On `stream.chunk` → append delta to matching event_id
6. On `timeline_event.completed` → finalize flow item content
7. On `stage.status` → update stage progress bar
8. On `execution.progress` → update per-agent progress phase text in stage display / parallel tabs
9. On `session.status` → optimistic status badge update, then background re-fetch `GET /sessions/:id` for full computed data (token counts, stage counts)

### Phase 7.5 (Trace View) — Segmented Control Navigation

The trace view is a separate route (`/sessions/:id/trace`) but shares the session header component. A segmented control (Conversation | Trace) in the header provides tab-like navigation between routes:

```
Session Detail Page (/sessions/:id):
┌────────────────────────────────────────┐
│ [← Back]  Session Header               │
│ [ Conversation | Trace ]  ← segmented  │
│     ^^^^^^^^^^^                         │
│ Conversation Timeline                   │
│ ...                                     │
│ Chat Panel                              │
└─────────────────────────────────────────┘

Trace Page (/sessions/:id/trace):
┌────────────────────────────────────────┐
│ [← Back]  Session Header               │
│ [ Conversation | Trace ]  ← segmented  │
│                  ^^^^^                  │
│ Trace Timeline (Accordions)             │
│ ...                                     │
└─────────────────────────────────────────┘
```

Both views share the same `SessionHeader` component with the segmented control. Clicking a segment navigates to the corresponding route. Visually feels like the old tab UX; under the hood they're separate pages with independent data loading (conversation loads timeline; trace loads trace list).
