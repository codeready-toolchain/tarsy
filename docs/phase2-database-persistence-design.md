# Phase 2: Database & Persistence - Detailed Design

## Overview

This document details the database and persistence layer design for the new TARSy implementation. We're using **Ent** as our ORM with **PostgreSQL** as the sole database backend, focusing on production-ready patterns from the start.

**Key Design Principles:**
- PostgreSQL-only (no SQLite compatibility layer)
- Ent ORM for type-safe, generated database access
- Service layer pattern (not repository pattern) 
- Context-based cancellation and timeouts throughout
- Database-backed job queue for session processing
- Built-in migration system via Ent/Atlas

**Major Design Changes from Old TARSy:**
- **Five-layer architecture**: Clean separation of Stage/AgentExecution/TimelineEvent/Message/Interactions
- **Lazy context building**: No `stage_output` or `agent_output` fields - context built on-demand
- **Linear message storage**: Eliminates O(n²) conversation duplication in LLMInteraction
- **WebSocket-friendly**: event_id tracking eliminates frontend de-duplication logic
- **Full-text search**: GIN indexes on alert_data and final_analysis for powerful search
- **Soft deletes**: Retention policy with restore capability (hard delete can be added later)
- **Event cleanup**: Automatic cleanup on session completion + TTL fallback
- **No pause feature**: Simplified by removing pause/resume complexity

---

## Architecture Overview

### System Architecture

```
┌─────────────────────────────────────────────────────────┐
│                     Go Service Layer                    │
│  (Business Logic, Transaction Management, Validation)   │
│                                                         │
│  • SessionService (CRUD, queue management)              │
│  • StageService (stage + agent execution)               │
│  • TimelineService (UX events)                          │
│  • MessageService (LLM context)                         │
│  • InteractionService (debug data)                      │
│  • ChatService (lazy context building)                  │
│  • EventService (WebSocket distribution)                │
└─────────────────────────────────┬───────────────────────┘
                                  │
                                  ▼
┌─────────────────────────────────────────────────────────┐
│                    Ent Client (Generated)               │
│        (Type-safe queries, hooks, transactions)         │
└─────────────────────────────────┬───────────────────────┘
                                  │
                                  ▼
┌─────────────────────────────────────────────────────────┐
│                   PostgreSQL Database                   │
│                                                         │
│  Core:        AlertSession                              │
│  Layer 0a:    Stage (configuration + coordination)      │
│  Layer 0b:    AgentExecution (individual work)          │
│  Layer 1:     TimelineEvent (UX timeline)               │
│  Layer 2:     Message (LLM conversation)                │
│  Layer 3-4:   LLM/MCPInteraction (debug)                │
│  Support:     Event, Chat, ChatUserMessage              │
└─────────────────────────────────────────────────────────┘
```

### Five-Layer Data Model

```
┌─────────────────────────────────────────────────────────────┐
│ AlertSession (session metadata, status, alert data)         │
│   chain_id (live lookup, no snapshot)                       │
│   deleted_at (soft delete)                                  │
└────────────────────────────┬────────────────────────────────┘
                             │
                             ▼
┌─────────────────────────────────────────────────────────────┐
│ Layer 0a: Stage (chain stage - configuration)               │
│   • Stage name, index, execution mode                       │
│   • expected_agent_count, parallel_type, success_policy     │
│   • Aggregated status from agent executions                 │
│   • NO OUTPUT FIELD - lazy context building                 │
└────────────────────────────┬────────────────────────────────┘
                             │ 1:N
                             ▼
┌─────────────────────────────────────────────────────────────┐
│ Layer 0b: AgentExecution (individual agent work)            │
│   • Agent name, index, status, timing                       │
│   • iteration_strategy                                      │
│   • NO OUTPUT FIELD - lazy context building                 │
└────┬────────────┬────────────┬────────────┬─────────────────┘
     │            │            │            │
     ▼            ▼            ▼            ▼
┌─────────┐  ┌─────────┐  ┌──────────┐  ┌──────────┐
│ Layer 1 │  │ Layer 2 │  │ Layer 3  │  │ Layer 4  │
│Timeline │  │ Message │  │   LLM    │  │   MCP    │
│  Event  │  │ (LLM    │  │Interact. │  │Interact. │
│  (UX)   │  │context) │  │ (debug)  │  │ (debug)  │
└─────────┘  └─────────┘  └──────────┘  └──────────┘

Key: Each AgentExecution produces:
- TimelineEvents (for Reasoning Tab, streamed in real-time)
- Messages (for LLM conversation context)
- LLMInteractions (for Debug Page, full API details)
- MCPInteractions (for Debug Page, full tool details)
```

### Benefits of Five-Layer Architecture

✅ **Clean Conceptual Model**
- Stage = Configuration + Coordination + Aggregated Results
- AgentExecution = Individual Agent Work
- TimelineEvent = UX Timeline
- Message = LLM Conversation Context
- LLMInteraction/MCPInteraction = Debug Details

✅ **Uniform Stage Model**
- No special "parent execution" entities
- Every stage has 1+ agent executions (single or parallel treated uniformly)

✅ **Solves O(n²) Storage Problem**
- Old: conversation field duplicates all messages in every iteration (20 iterations = 420 messages stored, should be 40!)
- New: Messages stored once in Message table (20 iterations = 40 messages ✓)

✅ **Eliminates Frontend De-duplication**
- Old: Stream chunks → Store DB record → Frontend must de-duplicate ✗
- New: Create TimelineEvent → Stream with event_id → Update same event ✓
- Frontend just updates existing event by ID!

✅ **Separates Concerns**
- Reasoning Tab → Query TimelineEvents (fast, clean UX, stage/agent grouping)
- Debug Tab → Query LLMInteraction + MCPInteraction (full technical details)
- LLM Context → Query Messages for execution (conversation building)
- Chain Logic → Agent.BuildStageContext() generates context on-demand (lazy evaluation)

✅ **Lazy Context Building**
- No stage_output or agent_output in database!
- Context generated on-demand when next stage needs it
- Each agent knows its own structure and formats appropriately
- No wasted computation if no next stage exists
- Works seamlessly with parallel agents (aggregate multiple executions)

✅ **Flexible Queries**
- Timeline for entire session, specific stage, or individual agent
- Messages scoped to agent execution (separate conversations)
- Debug data only loaded when needed (separate page)
- Stage aggregation from agent executions

---

## Database Schema Design

### Five-Layer Architecture Overview

The new schema implements a **five-layer architecture** that cleanly separates concerns:

```
Layer 0a: Stage (Configuration + Coordination)
Layer 0b: AgentExecution (Individual Agent Work)
Layer 1:  TimelineEvent (UX-focused Reasoning Timeline)
Layer 2:  Message (LLM Conversation Context)
Layer 3:  LLMInteraction (Debug/Observability)
Layer 4:  MCPInteraction (Debug/Observability)
```

**Key Design Principles:**
- **No output fields**: Stage context built lazily on-demand (no `stage_output` or `agent_output`)
- **Stage + AgentExecution hierarchy**: Every stage has 1+ agent executions (uniform model)
- **Separate UX and Debug**: TimelineEvent for users, Interactions for debugging
- **Linear storage**: Message table prevents O(n²) conversation duplication
- **WebSocket-friendly**: event_id tracking eliminates frontend de-duplication

---

### Core Entities (Ent Schema)

#### 1. **AlertSession**
Primary entity tracking alert processing sessions.

**Fields:**
```
session_id          string    PK, UUID
alert_data          TEXT      Original alert payload (full-text searchable)
agent_type          string    Agent type (e.g., 'kubernetes')
alert_type          string    Alert classification (indexed)
status              enum      Current status (indexed)
started_at          time.Time Indexed
completed_at        *time.Time
error_message       *string
final_analysis      *string   Investigation summary (full-text searchable)
executive_summary   *string   Brief summary of investigation
executive_summary_error *string
session_metadata    JSON
author              *string   From oauth2-proxy
runbook_url         *string
mcp_selection       JSON      MCP override config

chain_id            string    Chain identifier (indexed) - live lookup, no snapshot
current_stage_index *int
current_stage_id    *string

pod_id              *string   For multi-replica coordination
last_interaction_at *time.Time For orphan detection (indexed)
slack_message_fingerprint *string For Slack threading

deleted_at          *time.Time Soft delete for retention policy (indexed)
```

**Indexes:**
- `status` - For queue queries
- `agent_type` - Filtering
- `alert_type` - Filtering  
- `(status, started_at)` - Most common query pattern
- `(status, last_interaction_at)` - Orphan detection
- `chain_id` - Chain-based queries
- `deleted_at` (partial: WHERE deleted_at IS NOT NULL) - Soft delete queries

**Full-Text Search Indexes:**
- GIN index on `to_tsvector('english', alert_data)` - Search alerts
- GIN index on `to_tsvector('english', final_analysis)` - Search analyses

**Enums:**
- `Status`: `pending`, `in_progress`, `completed`, `failed`, `cancelled`, `timed_out`

**Edges:**
- `stages` → `Stage[]` (cascade delete)
- `agent_executions` → `AgentExecution[]` (cascade delete)
- `timeline_events` → `TimelineEvent[]` (cascade delete)
- `messages` → `Message[]` (cascade delete)
- `llm_interactions` → `LLMInteraction[]` (cascade delete)
- `mcp_interactions` → `MCPInteraction[]` (cascade delete)
- `chat` → `Chat` (optional, cascade delete)

**🔄 CHANGES FROM OLD TARSY:**
- ✅ **KEEP**: Core structure, status enum
- ⚠️ **CHANGE**: Remove `chain_definition` (use live lookup from registry)
- ⚠️ **CHANGE**: Remove `pause_metadata` (pause feature dropped)
- ⚠️ **CHANGE**: alert_data is TEXT not JSON (full-text search)
- ➕ **ADD**: `deleted_at` for soft delete retention policy
- ➕ **ADD**: Full-text search support with GIN indexes
- ➕ **ADD**: `timed_out` status (distinct from failed)

---

### MCP Selection (Per-Alert Override)

**Purpose:** Allows per-alert customization of MCP servers and tools, overriding chain defaults.

**chain_id vs mcp_selection:**
- `chain_id`: Defines agent chain + default MCP configuration (live lookup from registry)
- `mcp_selection`: Optional override that replaces default MCP config for this alert

**JSON Structure (Go types):**
```go
// pkg/models/mcp_selection.go

type MCPServerSelection struct {
    Name  string    `json:"name"`                    // MCP server ID
    Tools *[]string `json:"tools,omitempty"`         // Specific tools, nil = all tools
}

type NativeToolsConfig struct {
    GoogleSearch  *bool `json:"google_search,omitempty"`   // nil = provider default
    CodeExecution *bool `json:"code_execution,omitempty"`  // nil = provider default
    URLContext    *bool `json:"url_context,omitempty"`     // nil = provider default
}

type MCPSelectionConfig struct {
    Servers     []MCPServerSelection `json:"servers"`
    NativeTools *NativeToolsConfig   `json:"native_tools,omitempty"`
}
```

**Example JSON:**
```json
{
  "servers": [
    {
      "name": "kubernetes-server",
      "tools": ["kubectl-get", "kubectl-describe"]
    }
  ],
  "native_tools": {
    "google_search": true,
    "code_execution": false
  }
}
```

**Usage:**
- API accepts `mcp` field in alert payload
- Stored in `AlertSession.mcp_selection`
- Agent execution uses this instead of chain defaults if present
- Chat inherits from parent session

---

#### 2. **Stage** (Layer 0a)
Chain stage configuration and coordination (NOT individual agent work).

**Purpose:** Represents a stage in the processing chain, coordinates agent execution(s).

**Fields:**
```
stage_id            string    PK, UUID
session_id          string    FK → AlertSession (indexed)

// Stage Configuration
stage_name          string    "Initial Analysis", "Deep Dive", etc.
stage_index         int       Position in chain: 0, 1, 2... (indexed)

// Execution Mode
expected_agent_count int      How many agents (1 for single, N for parallel)
parallel_type       *enum     null if count=1, "multi_agent"/"replica" if count>1
success_policy      *enum     null if count=1, "all"/"any" if count>1

// Stage-Level Status & Timing (aggregated from agent executions)
status              enum      pending, active, completed, failed, timed_out, cancelled
started_at          *time.Time When first agent started
completed_at        *time.Time When stage finished (any terminal state)
duration_ms         *int      Total stage duration
error_message       *string   Aggregated error if stage failed/timed_out/cancelled

// Chat Context (if applicable)
chat_id             *string   FK → Chat
chat_user_message_id *string  FK → ChatUserMessage
```

**Indexes:**
- `(session_id, stage_index)` - Unique, stage ordering within session
- `stage_id` - Primary lookups

**Enums:**
- `StageStatus`: `pending`, `active`, `completed`, `failed`, `cancelled`, `timed_out`
- `ParallelType`: `multi_agent`, `replica`
- `SuccessPolicy`: `all`, `any`

**Edges:**
- `session` → `AlertSession`
- `agent_executions` → `AgentExecution[]` (one-to-many)
- `timeline_events` → `TimelineEvent[]`
- `messages` → `Message[]`
- `chat` → `Chat` (optional)
- `chat_user_message` → `ChatUserMessage` (optional)

**🆕 NEW ENTITY** - Replaces old StageExecution concept with clean separation of stage coordination vs agent work.

---

#### 3. **AgentExecution** (Layer 0b)
Individual agent work within a stage.

**Purpose:** Each stage has 1+ agent executions. This is where the actual work happens.

**Fields:**
```
execution_id        string    PK, UUID
stage_id            string    FK → Stage (indexed)
session_id          string    FK → AlertSession (indexed) - denormalized for performance

// Agent Details
agent_name          string    "KubernetesAgent", "ArgoCDAgent", etc.
agent_index         int       1 for single, 1-N for parallel (indexed)

// Execution Status & Timing
status              enum      pending, active, completed, failed, cancelled, timed_out
started_at          *time.Time
completed_at        *time.Time
duration_ms         *int
error_message       *string   Error details if failed

// Agent Configuration
iteration_strategy  string    "react", "native_thinking", etc. (for observability)
```

**Indexes:**
- `(stage_id, agent_index)` - Unique, agent ordering within stage
- `execution_id` - Primary lookups
- `session_id` - Session-wide queries

**Enums:**
- `AgentStatus`: `pending`, `active`, `completed`, `failed`, `cancelled`, `timed_out`

**Edges:**
- `stage` → `Stage`
- `session` → `AlertSession`
- `timeline_events` → `TimelineEvent[]`
- `messages` → `Message[]`
- `llm_interactions` → `LLMInteraction[]`
- `mcp_interactions` → `MCPInteraction[]`

**🆕 NEW ENTITY** - Each agent execution is its own entity, enabling clean parallel agent support.

---

#### 4. **TimelineEvent** (Layer 1)
User-facing investigation timeline (UX-focused, streamed in real-time).

**Purpose:** Reasoning timeline for the Reasoning Tab, streamed in real-time.

**Fields:**
```
event_id            string    PK, UUID
session_id          string    FK → AlertSession (indexed)
stage_id            string    FK → Stage (indexed) - Stage grouping
execution_id        string    FK → AgentExecution (indexed) - Which agent

// Timeline Ordering
sequence_number     int       Order in timeline

// Timestamps
created_at          time.Time Creation timestamp (indexed)
updated_at          time.Time Last update (for streaming)

// Event Details
event_type          enum      llm_thinking, llm_response, llm_tool_call,
                              mcp_tool_call, mcp_tool_summary,
                              user_question, executive_summary, final_analysis
status              enum      streaming, completed, failed, cancelled, timed_out
content             string    Event content (grows during streaming, updateable on completion)
metadata            JSON      Type-specific data (tool_name, server_name, etc.)

// Debug Links (set on completion)
llm_interaction_id  *string   Link to debug details
mcp_interaction_id  *string   Link to debug details
```

**Indexes:**
- `(session_id, sequence_number)` - Timeline ordering
- `(stage_id, sequence_number)` - Stage timeline grouping
- `(execution_id, sequence_number)` - Agent timeline filtering
- `event_id` - Updates by ID
- `created_at` - Chronological queries

**Enums:**
- `EventType`: `llm_thinking`, `llm_response`, `llm_tool_call`, `mcp_tool_call`, `mcp_tool_summary`, `user_question`, `executive_summary`, `final_analysis`
- `EventStatus`: `streaming`, `completed`, `failed`, `cancelled`, `timed_out`

**Edges:**
- `session` → `AlertSession`
- `stage` → `Stage`
- `agent_execution` → `AgentExecution`
- `llm_interaction` → `LLMInteraction` (optional link)
- `mcp_interaction` → `MCPInteraction` (optional link)

**Key Features:**
- ✅ Created IMMEDIATELY when streaming starts (not after completion)
- ✅ Updateable during streaming, immutable after completion
- ✅ Frontend uses event_id to track updates - NO de-duplication logic needed!
- ✅ Single writer per event (the agent generating it)

**🆕 NEW ENTITY** - Solves frontend de-duplication problem from old TARSy.

---

#### 5. **Message** (Layer 2)
LLM conversation history (LLM context building).

**Purpose:** Conversation history for LLM API calls.

**Fields:**
```
message_id          string    PK, UUID
session_id          string    FK → AlertSession (indexed)
stage_id            string    FK → Stage (indexed) - Stage scoping
execution_id        string    FK → AgentExecution (indexed) - Agent conversation

// Message Details
sequence_number     int       Execution-scoped order
role                enum      system, user, assistant
content             string    Message text
created_at          time.Time Indexed
```

**Indexes:**
- `(execution_id, sequence_number)` - Agent conversation order
- `(stage_id, execution_id)` - Stage + agent scoping

**Enums:**
- `MessageRole`: `system`, `user`, `assistant`

**Edges:**
- `session` → `AlertSession`
- `stage` → `Stage`
- `agent_execution` → `AgentExecution`

**Key Features:**
- ✅ Execution-scoped: Each agent has its own conversation
- ✅ Stage-scoped reset: Each stage starts with fresh context
- ✅ Immutable: Messages never updated once created
- ✅ Linear storage: O(n) not O(n²) - no duplication!

**🆕 NEW ENTITY** - Solves O(n²) conversation duplication problem from old TARSy.

---

#### 6. **LLMInteraction** (Layer 3)
Full technical details for LLM calls (Debug Tab - Observability).

**Purpose:** Complete LLM API call records for debugging and analysis.

**Fields:**
```
interaction_id      string    PK, UUID
session_id          string    FK → AlertSession (indexed)
stage_id            string    FK → Stage (indexed)
execution_id        string    FK → AgentExecution (indexed) - Which agent

// Timing
created_at          time.Time Indexed

// Interaction Details
interaction_type    enum      iteration, final_analysis, executive_summary, chat_response
model_name          string    "gemini-2.0-flash-thinking-exp", etc.

// Conversation Context (links to Message table)
last_message_id     *string   FK → Message (last message sent to LLM)

// Full API Details
llm_request         JSON      Full API request payload
llm_response        JSON      Full API response payload
thinking_content    *string   Native thinking (Gemini)
response_metadata   JSON      Grounding, tool usage, etc.

// Metrics & Result
input_tokens        *int
output_tokens       *int
total_tokens        *int
duration_ms         *int
error_message       *string   null = success, not-null = failed
```

**Indexes:**
- `(execution_id, created_at)` - Agent's LLM calls chronologically
- `(stage_id, created_at)` - Stage's LLM calls
- `interaction_id` - Primary lookups

**Enums:**
- `InteractionType`: `iteration`, `final_analysis`, `executive_summary`, `chat_response`

**Edges:**
- `session` → `AlertSession`
- `stage` → `Stage`
- `agent_execution` → `AgentExecution`
- `last_message` → `Message` (optional)

**Key Features:**
- ✅ Created on completion (not during streaming)
- ✅ Immutable: Full technical record for audit
- ✅ Links to Messages: Conversation reconstructed via `last_message_id`
- ✅ Full API payloads: Request/response for debugging
- ✅ Success/Failure: Determined by `error_message` (null = success)

**🔄 CHANGES FROM OLD TARSY:**
- ⚠️ **CHANGE**: Remove `conversation` field - use Message table + `last_message_id`
- ⚠️ **CHANGE**: Link to Stage + AgentExecution (not old StageExecution)
- ➕ **ADD**: `last_message_id` for conversation reconstruction

---

#### 7. **MCPInteraction** (Layer 4)
Full technical details for MCP tool calls (Debug Tab - Observability).

**Purpose:** Complete MCP tool call records for debugging and analysis.

**Fields:**
```
interaction_id      string    PK, UUID
session_id          string    FK → AlertSession (indexed)
stage_id            string    FK → Stage (indexed)
execution_id        string    FK → AgentExecution (indexed) - Which agent

// Timing
created_at          time.Time Indexed

// Interaction Details
interaction_type    enum      tool_call, tool_list
server_name         string    "kubernetes", "argocd", etc.
tool_name           *string   "kubectl_get_pods", etc.

// Full Details
tool_arguments      JSON      Input parameters
tool_result         JSON      Tool output
available_tools     JSON      For tool_list type

// Result & Timing
duration_ms         *int
error_message       *string   null = success, not-null = failed
```

**Indexes:**
- `(execution_id, created_at)` - Agent's MCP calls chronologically
- `(stage_id, created_at)` - Stage's MCP calls
- `interaction_id` - Primary lookups

**Enums:**
- `InteractionType`: `tool_call`, `tool_list`

**Edges:**
- `session` → `AlertSession`
- `stage` → `Stage`
- `agent_execution` → `AgentExecution`

**Key Features:**
- ✅ Created on completion (not during streaming)
- ✅ Immutable: Full technical record for audit
- ✅ Full API payloads: Request/response for debugging
- ✅ Success/Failure: Determined by `error_message` (null = success)

**🔄 CHANGES FROM OLD TARSY:**
- ⚠️ **CHANGE**: Link to Stage + AgentExecution (not old StageExecution)
- ⚠️ **CHANGE**: Simplified interaction types (removed server_init, server_error)

---

#### 8. **Event**
Event persistence for cross-pod distribution and catchup.

**Fields:**
```
id                  int       PK, Auto-increment
session_id          string    FK → AlertSession (indexed) - for cleanup
channel             string    Event channel (indexed)
payload             JSON      Event data
created_at          time.Time Auto-set (indexed)
```

**Indexes:**
- `channel` - Channel filtering
- `session_id` - Session-based cleanup
- `created_at` - Cleanup queries
- `(channel, id)` - Polling queries

**Edges:**
- `session` → `AlertSession`

**Retention Strategy:**
- Events only needed for active sessions
- Clean up automatically on session completion
- Fallback: TTL-based cleanup (7 days) for orphaned events

**🔄 CHANGES FROM OLD TARSY:**
- ➕ **ADD**: `session_id` for targeted cleanup
- ➕ **ADD**: Automatic cleanup on session completion

---

#### 9. **Chat**
Chat metadata for follow-up conversations.

**Fields:**
```
chat_id             string    PK, UUID
session_id          string    FK → AlertSession (unique, indexed)
created_at          time.Time
created_by          *string   User email
chain_id            string    From original session (live lookup, no snapshot)
pod_id              *string   For multi-replica
last_interaction_at *time.Time For orphan detection
```

**Indexes:**
- `session_id` - Session lookup (unique)
- `created_at` - Listing
- `(pod_id, last_interaction_at)` - Orphan detection

**Edges:**
- `session` → `AlertSession` (unique)
- `user_messages` → `ChatUserMessage[]` (cascade delete)
- `stages` → `Stage[]` (chat stages)

**Note on MCP Configuration:**
- `mcp_selection` queried from parent `session.mcp_selection` (not duplicated)
- Chat inherits user's original tool selection via session FK

**🔄 CHANGES FROM OLD TARSY:**
- ⚠️ **CHANGE**: Remove `conversation_history` (build context lazily from artifacts)
- ⚠️ **CHANGE**: Remove `context_captured_at` (not needed with lazy building)
- ⚠️ **CHANGE**: Remove `mcp_selection` (query from session instead)
- ✅ **KEEP**: Core chat structure

---

#### 10. **ChatUserMessage**
User messages in chat conversations.

**Fields:**
```
message_id          string    PK, UUID
chat_id             string    FK → Chat (indexed)
content             string    Question text
author              string    User email
created_at          time.Time Indexed
```

**Indexes:**
- `chat_id` - Chat lookup
- `created_at` - Message ordering

**Edges:**
- `chat` → `Chat`
- `stage` → `Stage` (response stage)

**🔄 CHANGES FROM OLD TARSY:**
- ✅ **KEEP**: Same structure

---

## Timeline & Debug Page Architecture

### Design Decision: Separate Pages (Not Tabs)

Split into two independent pages for better performance and separation of concerns.

#### Main Session Page: `/sessions/{session_id}`

**What it shows:**
- Session metadata (status, duration, summary)
- Reasoning timeline (TimelineEvents)
- Real-time progress during active session

**API Endpoints:**
```
GET /api/sessions/{id}  → Session metadata + TimelineEvents
```

**WebSocket:**
```
/ws/sessions/{id}  → TimelineEvent updates (create/update)
```

**Performance:**
- ✅ Single table query (no joins/merging needed)
- ✅ Indexed by `(session_id, sequence_number)`
- ✅ Real-time streaming via WebSocket during active session
- ✅ Fast initial page load (no debug data)
- ✅ Serves 95% of users' needs

**Query Examples:**
```go
// Timeline for entire session
events := client.TimelineEvent.Query().
    Where(timelineevent.SessionIDEQ(sessionID)).
    Order(ent.Asc(timelineevent.FieldSequenceNumber)).
    All(ctx)

// Timeline for a stage (all agents combined)
events := client.TimelineEvent.Query().
    Where(timelineevent.StageIDEQ(stageID)).
    Order(ent.Asc(timelineevent.FieldSequenceNumber)).
    All(ctx)

// Timeline for specific agent in parallel stage
events := client.TimelineEvent.Query().
    Where(timelineevent.ExecutionIDEQ(executionID)).
    Order(ent.Asc(timelineevent.FieldSequenceNumber)).
    All(ctx)
```

---

#### Debug Page: `/sessions/{session_id}/debug`

**What it shows:**
- LLM Interactions (collapsed list)
- MCP Interactions (collapsed list)
- Detailed request/response data on expand

**Two-level loading pattern:**

**Level 1: List View (Initial Page Load)**
```sql
-- Just metadata for collapsed view
SELECT 
  interaction_id, 
  interaction_type, 
  created_at,
  model_name,           -- for LLM
  server_name,          -- for MCP
  duration_ms,
  error_message
FROM llm_interactions 
WHERE session_id = ? 
ORDER BY created_at ASC;
```

**Level 2: Detail View (On User Expand)**
```sql
-- Full data when user expands an interaction
SELECT * FROM llm_interactions 
WHERE interaction_id = ?;
```

**API Endpoints:**
```
GET /api/sessions/{id}/debug                      → Interaction list (metadata only)
GET /api/sessions/{id}/debug/llm/{interaction_id} → Full LLM interaction details
GET /api/sessions/{id}/debug/mcp/{interaction_id} → Full MCP interaction details
```

**WebSocket:**
```
/ws/sessions/{id}/debug  → Lightweight interaction.created events
```

**WebSocket Event Example:**
```json
{
  "type": "interaction.created",
  "interaction_id": "abc123",
  "interaction_type": "iteration",
  "created_at": "2026-02-03T10:30:00Z"
}
```
- Frontend adds collapsed item to list
- Full interaction data loaded from API when user expands

**Performance:**
- ✅ **List view**: Very fast (no large JSON fields, just metadata)
- ✅ **Detail view**: Lazy loaded only when needed (user expands interaction)
- ✅ **Bandwidth**: Only load full request/response JSON when user wants to see it
- ✅ **Only loaded when needed**: Most users never visit debug page

**Conversation Reconstruction:**
```go
// Get the last message that was sent to LLM
lastMessage := client.Message.Get(ctx, interaction.LastMessageID)

// Get all messages up to and including that sequence number
messages := client.Message.Query().
    Where(message.ExecutionIDEQ(interaction.ExecutionID)).
    Where(message.SequenceNumberLTE(lastMessage.SequenceNumber)).
    Order(ent.Asc(message.FieldSequenceNumber)).
    All(ctx)

// These are the exact messages sent as input to this LLM call
```

---

### Benefits of Separate Pages

- ✅ **Much faster main page load**: Only loads what 95% of users need
- ✅ **Cleaner separation**: Reasoning and Debug are truly independent
- ✅ **Better performance**: Only pay for what you use
- ✅ **Simpler implementation**: No tab state management, clear API endpoints
- ✅ **Better for most users**: Debug data only loaded when explicitly navigated to
- ✅ **Independent WebSocket subscriptions**: Each page subscribes to only what it needs

---

## Stage Status Aggregation Logic

**Key Rule:** Stage remains `active` while ANY agent is `pending` or `active`. Stage status is only determined when ALL agents have terminated.

### Agent Statuses

- `pending`: Not yet started (initial state)
- `active`: Currently executing
- `completed`: Finished successfully
- `failed`: Failed with error
- `timed_out`: Exceeded timeout limit
- `cancelled`: Manually cancelled

**Terminal States:** `completed`, `failed`, `timed_out`, `cancelled`

### Aggregation Rules (when all agents terminated)

**For `success_policy = "all"`** (all agents must succeed):
1. If ALL agents `completed` → Stage `completed`
2. Otherwise:
   - If ALL agents `timed_out` → Stage `timed_out`
   - If ALL agents `cancelled` → Stage `cancelled`
   - Mixed failures → Pick dominant failure type (implementation-specific)

**For `success_policy = "any"`** (at least one agent must succeed):
1. If ANY agent `completed` → Stage `completed` (even if others failed/timed_out/cancelled)
2. Otherwise (all failed):
   - If ALL agents `timed_out` → Stage `timed_out`
   - If ALL agents `cancelled` → Stage `cancelled`
   - If at least one agent `completed` → Stage `completed`
   - Mixed failures (no agent `completed`) → Pick dominant failure type (implementation-specific)

**Stage stays `active`** while ANY agent is `pending` or `active`

### Implementation Example

```go
func (s *StageService) AggregateStageStatus(ctx context.Context, stageID string) error {
    // Get stage with its agent executions
    stage, err := s.client.Stage.Query().
        Where(stage.StageIDEQ(stageID)).
        WithAgentExecutions().
        Only(ctx)
    if err != nil {
        return err
    }
    
    // Check if any agent is still pending or active
    for _, exec := range stage.Edges.AgentExecutions {
        if exec.Status == agentexecution.StatusPending || 
           exec.Status == agentexecution.StatusActive {
            // Stage remains active
            return nil
        }
    }
    
    // All agents terminated - determine final stage status
    successPolicy := stage.SuccessPolicy
    
    if successPolicy == stage.SuccessPolicyAll {
        // All must succeed
        allCompleted := true
        for _, exec := range stage.Edges.AgentExecutions {
            if exec.Status != agentexecution.StatusCompleted {
                allCompleted = false
                break
            }
        }
        
        if allCompleted {
            return stage.Update().
                SetStatus(stage.StatusCompleted).
                SetCompletedAt(time.Now()).
                Exec(ctx)
        }
        
        // Determine failure type
        // (implementation-specific logic for mixed failures)
        
    } else if successPolicy == stage.SuccessPolicyAny {
        // At least one must succeed
        anyCompleted := false
        for _, exec := range stage.Edges.AgentExecutions {
            if exec.Status == agentexecution.StatusCompleted {
                anyCompleted = true
                break
            }
        }
        
        if anyCompleted {
            return stage.Update().
                SetStatus(stage.StatusCompleted).
                SetCompletedAt(time.Now()).
                Exec(ctx)
        }
        
        // All failed - determine failure type
        // (implementation-specific logic)
    }
    
    return nil
}
```

---

## Event Cleanup Strategy

### Retention Policy

**Context:**
- `Event` table used for WebSocket event distribution to live clients during active sessions
- Events only needed for **active sessions**
- Used **only for live updates** (not historical replay)
- No need to retain after session completes

### Primary: Automatic Cleanup on Session Completion

```go
// When session reaches terminal state (completed, failed, cancelled, timed_out)
func (s *SessionService) CompleteSession(ctx context.Context, sessionID string) error {
    // Update session status
    err := s.client.AlertSession.
        UpdateOneID(sessionID).
        SetStatus(alertsession.StatusCompleted).
        SetCompletedAt(time.Now()).
        Exec(ctx)
    
    if err != nil {
        return err
    }
    
    // Clean up events for this session
    _, err = s.client.Event.
        Delete().
        Where(event.SessionIDEQ(sessionID)).
        Exec(ctx)
    
    return err
}
```

### Fallback: TTL-based Cleanup

```go
// Add created_at timestamp to Event schema (already included)
// Scheduled cleanup job (e.g., every hour via cron)
func cleanupOldEvents(ctx context.Context, client *ent.Client) error {
    cutoff := time.Now().Add(-7 * 24 * time.Hour)
    
    deleted, err := client.Event.
        Delete().
        Where(event.CreatedAtLT(cutoff)).
        Exec(ctx)
    
    log.Printf("Cleaned up %d old events (older than 7 days)", deleted)
    return err
}
```

**Expected Size:**
- Active sessions at any time: ~10-100
- Events per session: ~50-200
- Total events in table: < 20K rows (very manageable)

---

## Lazy Context Building Pattern

**Key Design Decision:** No `stage_output` or `agent_output` fields in the database!

### Why No Output Fields?

**Problems with storing output:**
1. ❌ **Premature generation**: Stage doesn't know what next stage needs
2. ❌ **Wasted computation**: Generated even if no next stage exists
3. ❌ **One-size-fits-all**: Can't customize for different consumers

**Solution: Lazy Context Building**

Each agent type implements a `BuildStageContext()` method that:
- Queries its own artifacts (Messages, TimelineEvents, LLMInteractions)
- Formats them appropriately for consumption by next stage
- Called **on-demand** only when next stage actually needs it

### Agent Interface

```go
type Agent interface {
    // Execute the agent
    Execute(ctx context.Context, sessionCtx SessionContext, prevStageContext string) error
    
    // Build context from THIS agent's completed stage
    // Called by next stage when it needs context (lazy evaluation)
    BuildStageContext(ctx context.Context, stageID string) (string, error)
}
```

### Single Agent Example

```go
// KubernetesAgent knows its own structure
func (a *KubernetesAgent) BuildStageContext(ctx context.Context, stageID string) (string, error) {
    // Query this stage's artifacts
    events := a.db.TimelineEvent.Query().
        Where(timelineevent.StageIDEQ(stageID)).
        Order(ent.Asc(timelineevent.FieldSequenceNumber)).
        All(ctx)
    
    messages := a.db.Message.Query().
        Where(message.StageIDEQ(stageID)).
        Order(ent.Asc(message.FieldSequenceNumber)).
        All(ctx)
    
    // Format in KubernetesAgent's own way
    var sb strings.Builder
    sb.WriteString("=== Kubernetes Analysis Results ===\n\n")
    
    // Extract thinking
    for _, event := range events {
        if event.EventType == "llm_thinking" {
            sb.WriteString(fmt.Sprintf("Thinking: %s\n", event.Content))
        }
    }
    
    // Extract tool calls
    for _, event := range events {
        if event.EventType == "mcp_tool_call" {
            sb.WriteString(fmt.Sprintf("Tool %s: %s\n", 
                event.Metadata["tool_name"], event.Content))
        }
    }
    
    // Extract final analysis
    for _, event := range events {
        if event.EventType == "final_analysis" {
            sb.WriteString(fmt.Sprintf("\nConclusion: %s\n", event.Content))
        }
    }
    
    return sb.String(), nil
}
```

### Parallel Agents Example

When stage has multiple parallel agents, aggregate all their outputs:

```go
// SynthesisAgent builds context from stage with parallel agents
func (a *SynthesisAgent) BuildStageContext(ctx context.Context, stageID string) (string, error) {
    // Get all agent executions for this stage
    executions := a.db.AgentExecution.Query().
        Where(agentexecution.StageIDEQ(stageID)).
        Order(ent.Asc(agentexecution.FieldAgentIndex)).
        All(ctx)
    
    var sb strings.Builder
    sb.WriteString("=== Synthesis of Parallel Analysis ===\n\n")
    
    // Aggregate context from each agent execution
    for _, exec := range executions {
        sb.WriteString(fmt.Sprintf("--- %s (Agent %d) ---\n", exec.AgentName, exec.AgentIndex))
        
        // Get this agent's timeline events
        events := a.db.TimelineEvent.Query().
            Where(timelineevent.ExecutionIDEQ(exec.ExecutionID)).
            Order(ent.Asc(timelineevent.FieldSequenceNumber)).
            All(ctx)
        
        // Extract final analysis from each agent
        for _, event := range events {
            if event.EventType == "final_analysis" {
                sb.WriteString(fmt.Sprintf("%s\n\n", event.Content))
            }
        }
    }
    
    return sb.String(), nil
}
```

### Chain Orchestrator Usage

```go
func (c *ChainOrchestrator) ExecuteStage(ctx context.Context, stage StageConfig, prevStageID *string) error {
    var prevContext string
    
    if prevStageID != nil {
        // Lookup which agent type ran previous stage
        prevStage := c.db.Stage.Query().Where(stage.StageIDEQ(*prevStageID)).Only(ctx)
        
        // Get any execution from that stage to determine agent type
        prevExecution := c.db.AgentExecution.Query().
            Where(agentexecution.StageIDEQ(*prevStageID)).
            First(ctx)
        
        // Create agent instance to use its context builder
        prevAgent := c.CreateAgent(prevExecution.AgentName)
        
        // Lazy evaluation - generate context NOW (not at stage completion!)
        prevContext, _ = prevAgent.BuildStageContext(ctx, *prevStageID)
    }
    
    // Execute current stage with context from previous
    agent := c.CreateAgent(stage.AgentName)
    return agent.Execute(ctx, sessionCtx, prevContext)
}
```

### Benefits

✅ **No wasted computation**: Only generate when actually needed  
✅ **No premature decisions**: Next stage specifies what it needs  
✅ **Encapsulation**: Each agent knows its own structure  
✅ **Flexibility**: Can change formatting without schema changes  
✅ **Simpler schema**: No JSON output fields to maintain  
✅ **Works with parallel agents**: Aggregate multiple executions seamlessly

---

## Migration Strategy

### Ent Migration System

Ent uses **Atlas** for migrations, which is declarative and type-safe.

**Approach:**
1. **Auto-generation**: Ent generates migrations from schema changes
2. **Versioned migrations**: Stored in `ent/migrate/migrations/`
3. **Runtime application**: Migrations run on service startup
4. **Rollback support**: Via Atlas versioning

**Migration Files Structure:**
```
ent/
├── schema/          # Schema definitions (source of truth)
│   ├── alertsession.go
│   ├── stage.go
│   ├── agentexecution.go
│   ├── timelineevent.go
│   ├── message.go
│   ├── llminteraction.go
│   ├── mcpinteraction.go
│   └── ...
├── migrate/
│   ├── migrations/  # Generated SQL migrations
│   │   ├── 20260203100000_init.sql
│   │   ├── 20260204120000_add_chat.sql
│   │   └── atlas.sum
│   └── migrate.go   # Migration runner
```

**Migration Commands:**
```bash
# Generate new migration from schema changes
go run -mod=mod entgo.io/ent/cmd/ent generate ./ent/schema

# Create migration file
atlas migrate diff migration_name \
  --dir "file://ent/migrate/migrations" \
  --to "ent://ent/schema" \
  --dev-url "docker://postgres/15/dev?search_path=public"

# Apply migrations (in code)
client.Schema.Create(ctx,
  migrate.WithDir("file://ent/migrate/migrations"),
)
```

**🔄 CHANGES FROM OLD TARSY:**
- ⚠️ **CHANGE**: Atlas instead of Alembic (Go-native, better)
- ➕ **IMPROVE**: Declarative vs. imperative migrations
- ➕ **IMPROVE**: Type-safe, generated from code

---

## Service Layer Design

### Database Client Initialization

```go
// pkg/database/client.go

package database

import (
    "context"
    stdsql "database/sql"
    "embed"
    "errors"
    "fmt"
    "io/fs"
    "time"
    
    "entgo.io/ent/dialect"
    entsql "entgo.io/ent/dialect/sql"
    "github.com/codeready-toolchain/tarsy/ent"
    "github.com/golang-migrate/migrate/v4"
    "github.com/golang-migrate/migrate/v4/database/postgres"
    "github.com/golang-migrate/migrate/v4/source/iofs"
    _ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations
var migrationsFS embed.FS

type Config struct {
    Host     string
    Port     int
    User     string
    Password string
    Database string
    SSLMode  string
    
    // Connection pool settings
    MaxOpenConns    int
    MaxIdleConns    int
    ConnMaxLifetime time.Duration
    ConnMaxIdleTime time.Duration
}

// Client wraps Ent client and provides access to the underlying database
type Client struct {
    *ent.Client
    db *stdsql.DB
}

func NewClient(ctx context.Context, cfg Config) (*Client, error) {
    dsn := fmt.Sprintf(
        "host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
        cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database, cfg.SSLMode,
    )
    
    // Open database connection using pgx driver
    db, err := stdsql.Open("pgx", dsn)
    if err != nil {
        return nil, fmt.Errorf("failed to open database: %w", err)
    }
    
    // Configure connection pool
    db.SetMaxOpenConns(cfg.MaxOpenConns)
    db.SetMaxIdleConns(cfg.MaxIdleConns)
    db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
    db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
    
    // Create Ent driver from existing database connection
    drv := entsql.OpenDB(dialect.Postgres, db)
    entClient := ent.NewClient(ent.Driver(drv))
    
    // Run migrations
    if err := runMigrations(ctx, db, cfg, drv, entClient); err != nil {
        entClient.Close()
        return nil, fmt.Errorf("failed to run migrations: %w", err)
    }
    
    return &Client{Client: entClient, db: db}, nil
}

// runMigrations runs database migrations using golang-migrate with embedded migration files,
// or falls back to Ent's auto-migration for initial setup.
//
// Migration workflow:
//   1. Developer changes schema: Edit ent/schema/*.go
//   2. Generate migration: make migrate-create NAME=add_feature
//   3. Migrations saved to pkg/database/migrations/*.sql
//   4. Files embedded into binary at compile time (go:embed)
//   5. Review & commit: Check SQL files, commit to git
//   6. Deploy: Build binary (migrations embedded automatically)
//   7. Auto-apply: App applies pending migrations on startup
//
// For initial setup (before first migration is generated):
//   - Uses Ent's Schema.Create() to initialize database from schema definitions
func runMigrations(ctx context.Context, db *stdsql.DB, cfg Config, 
                   drv *entsql.Driver, entClient *ent.Client) error {
    // Check if embedded migrations exist
    hasMigrations, err := hasEmbeddedMigrations()
    if err != nil {
        return fmt.Errorf("failed to check embedded migrations: %w", err)
    }
    
    if hasMigrations {
        // Use golang-migrate with embedded migrations
        driver, err := postgres.WithInstance(db, &postgres.Config{})
        if err != nil {
            return fmt.Errorf("failed to create postgres driver: %w", err)
        }
        
        sourceDriver, err := iofs.New(migrationsFS, "migrations")
        if err != nil {
            return fmt.Errorf("failed to create migration source: %w", err)
        }
        
        m, err := migrate.NewWithInstance("iofs", sourceDriver, cfg.Database, driver)
        if err != nil {
            return fmt.Errorf("failed to create migrate instance: %w", err)
        }
        
        // Apply all pending migrations
        err = m.Up()
        if err != nil && err != migrate.ErrNoChange {
            return fmt.Errorf("failed to apply migrations: %w", err)
        }
        
        // Close the migrate instance to avoid resource leaks
        if srcErr, dbErr := m.Close(); srcErr != nil || dbErr != nil {
            if srcErr != nil {
                return fmt.Errorf("failed to close migration source: %w", srcErr)
            }
            return fmt.Errorf("failed to close migration database: %w", dbErr)
        }
    } else {
        // Fall back to auto-migration for initial setup
        if err := entClient.Schema.Create(ctx); err != nil {
            return fmt.Errorf("failed to create schema: %w", err)
        }
    }
    
    return nil
}

func hasEmbeddedMigrations() (bool, error) {
    entries, err := fs.ReadDir(migrationsFS, "migrations")
    if err != nil {
        if errors.Is(err, fs.ErrNotExist) {
            return false, nil
        }
        return false, fmt.Errorf("failed to read embedded migrations: %w", err)
    }
    
    // Check if there are any .sql files
    for _, entry := range entries {
        if !entry.IsDir() && len(entry.Name()) > 4 && 
           entry.Name()[len(entry.Name())-4:] == ".sql" {
            return true, nil
        }
    }
    
    return false, nil
}
```

**🔄 CHANGES FROM OLD TARSY:**
- ⚠️ **CHANGE**: Ent client instead of SQLModel session
- ➕ **IMPROVE**: Connection pool configuration in one place
- ➕ **ADD**: Explicit context support throughout

---

### Service Layer Pattern

**Structure:**
```go
// pkg/services/session_service.go

type SessionService struct {
    client *ent.Client
}

func NewSessionService(client *ent.Client) *SessionService {
    return &SessionService{client: client}
}

// CreateSession creates a new alert session with initial stage
func (s *SessionService) CreateSession(
    ctx context.Context,
    req CreateSessionRequest,
) (*ent.AlertSession, error) {
    // Business logic with transaction
    tx, err := s.client.Tx(ctx)
    if err != nil {
        return nil, err
    }
    defer tx.Rollback()
    
    // Create session
    session, err := tx.AlertSession.Create().
        SetSessionID(req.SessionID).
        SetAlertData(req.AlertData).
        SetAgentType(req.AgentType).
        SetStatus(alertsession.StatusPending).
        SetChainID(req.ChainID). // No chain_definition - live lookup!
        Save(ctx)
    if err != nil {
        return nil, err
    }
    
    // Create initial stage with agent execution (if needed)
    // Example: First stage with single agent
    stage, err := tx.Stage.Create().
        SetSession(session).
        SetStageName("Initial Analysis").
        SetStageIndex(0).
        SetExpectedAgentCount(1).
        SetStatus(stage.StatusPending).
        Save(ctx)
    if err != nil {
        return nil, err
    }
    
    // Create agent execution for the stage
    _, err = tx.AgentExecution.Create().
        SetStage(stage).
        SetSession(session).
        SetAgentName("KubernetesAgent").
        SetAgentIndex(1).
        SetStatus(agentexecution.StatusPending).
        SetIterationStrategy("react").
        Save(ctx)
    if err != nil {
        return nil, err
    }
    
    return session, tx.Commit()
}

// ClaimNextPendingSession atomically claims a pending session
func (s *SessionService) ClaimNextPendingSession(
    ctx context.Context,
    podID string,
) (*ent.AlertSession, error) {
    tx, err := s.client.Tx(ctx)
    if err != nil {
        return nil, err
    }
    defer tx.Rollback()
    
    // PostgreSQL: Use FOR UPDATE SKIP LOCKED
    session, err := tx.AlertSession.Query().
        Where(
            alertsession.StatusEQ(alertsession.StatusPending),
        ).
        Order(ent.Asc(alertsession.FieldStartedAt)).
        ForUpdate(
            sql.WithLockAction(sql.SkipLocked),
        ).
        First(ctx)
    
    if err != nil {
        if ent.IsNotFound(err) {
            return nil, nil // No pending sessions
        }
        return nil, err
    }
    
    // Update to claimed status
    session, err = session.Update().
        SetStatus(alertsession.StatusInProgress).
        SetPodID(podID).
        SetLastInteractionAt(time.Now()).
        Save(ctx)
    if err != nil {
        return nil, err
    }
    
    return session, tx.Commit()
}
```

**🔄 CHANGES FROM OLD TARSY:**
- ⚠️ **CHANGE**: Service methods instead of repository pattern
- ➕ **IMPROVE**: Type-safe queries via Ent
- ➕ **IMPROVE**: Explicit transaction handling
- ✅ **KEEP**: Same business logic (claim, update, etc.)

---

## Context & Cancellation Support

### Strategy: Context by Operation Type

**Different contexts for different scenarios to prevent inconsistent state.**

#### 1. Read Operations (Safe to Cancel)

Use HTTP request context - safe to cancel if user closes browser:

```go
// List sessions - can be cancelled
func (s *SessionService) ListSessions(ctx context.Context) ([]*ent.AlertSession, error) {
    return s.client.AlertSession.Query().
        Where(alertsession.DeletedAtIsNil()).
        All(ctx)  // OK: User cancels, query stops
}

// Get session details - can be cancelled
func (s *SessionService) GetSession(ctx context.Context, sessionID string) (*ent.AlertSession, error) {
    return s.client.AlertSession.Query().
        Where(alertsession.SessionIDEQ(sessionID)).
        First(ctx)  // OK: Read operation
}
```

#### 2. Background Processing (Detached from HTTP)

Use background context, only respect shutdown signal:

```go
// Investigation worker - runs independently
func (w *SessionWorker) processNext(shutdownCtx context.Context) error {
    // Create background context for DB - not tied to any HTTP request
    dbCtx := context.Background()
    
    // Claim session (critical operation)
    session, err := w.sessionService.ClaimNextPendingSession(dbCtx, w.podID)
    if err != nil {
        return err
    }
    
    // Execute investigation (detached from HTTP)
    // Only check shutdownCtx for graceful shutdown
    select {
    case <-shutdownCtx.Done():
        return shutdownCtx.Err()
    default:
        return w.executeSession(dbCtx, session)
    }
}
```

#### 3. Critical Writes (Timeout but Not HTTP Cancellation)

Use timeout context for deadlock detection, but don't inherit HTTP cancellation:

```go
// Create session - critical write with timeout protection
func (s *SessionService) CreateSession(
    httpCtx context.Context,  // Accept but don't use directly
    req CreateSessionRequest,
) (*ent.AlertSession, error) {
    // Create separate context with timeout (not tied to HTTP request)
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    
    tx, err := s.client.Tx(ctx)
    if err != nil {
        return nil, err
    }
    defer tx.Rollback()  // Always rollback uncommitted
    
    session, err := tx.AlertSession.Create().
        SetSessionID(req.SessionID).
        SetAlertData(req.AlertData).
        Save(ctx)
    if err != nil {
        return nil, err
    }
    
    return session, tx.Commit()
}
```

#### 4. All Transactions (Explicit Rollback)

Always use defer rollback pattern:

```go
func (s *SessionService) UpdateSessionWithStage(ctx context.Context, ...) error {
    tx, err := s.client.Tx(ctx)
    if err != nil {
        return err
    }
    defer tx.Rollback()  // CRITICAL: Rollback if commit not reached
    
    // Multiple operations...
    
    return tx.Commit()  // Only commit if everything succeeded
}
```

### Benefits

- ✅ **Read safety**: Cancelled reads don't harm system
- ✅ **Write protection**: Critical writes complete or rollback cleanly
- ✅ **Background isolation**: Investigations run independently of HTTP
- ✅ **Deadlock prevention**: Timeouts on all critical operations
- ✅ **Clean shutdown**: Respect shutdown signal without data corruption

### Anti-Patterns to Avoid

```go
// ❌ BAD: HTTP context on critical write
func (s *SessionService) CreateSession(ctx context.Context, ...) {
    // User closes browser → transaction cancelled → partial state!
    tx, err := s.client.Tx(ctx)  // DON'T USE HTTP CONTEXT HERE
}

// ❌ BAD: No timeout on critical operation
func (s *SessionService) ClaimSession(ctx context.Context, ...) {
    // Could wait forever if deadlock
    session, err := s.client.AlertSession.Query().
        ForUpdate().First(ctx)  // NEED TIMEOUT
}

// ❌ BAD: Missing defer rollback
func (s *SessionService) UpdateSession(ctx context.Context, ...) {
    tx, err := s.client.Tx(ctx)
    // Missing defer tx.Rollback() - could leak uncommitted transaction!
}
```

**🔄 CHANGES FROM OLD TARSY:**
- ➕ **ADD**: Context strategy by operation type
- ➕ **IMPROVE**: Protect critical writes from HTTP cancellation
- ➕ **IMPROVE**: Background processing isolation
- ➕ **ADD**: Explicit timeout patterns for deadlock prevention

---

## Queue Management

### Database-Backed Queue

**Design:**
- Use `AlertSession.status` as queue state
- PostgreSQL `FOR UPDATE SKIP LOCKED` for claiming
- Pod-based coordination via `pod_id` field

**Queue States:**
```
pending → in_progress → completed/failed/cancelled
                     ↓
                   paused
```

**Worker Pattern:**
```go
// pkg/worker/session_worker.go

type SessionWorker struct {
    sessionService *services.SessionService
    podID          string
    pollInterval   time.Duration
}

func (w *SessionWorker) Run(ctx context.Context) error {
    ticker := time.NewTicker(w.pollInterval)
    defer ticker.Stop()
    
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
            
        case <-ticker.C:
            if err := w.processNext(ctx); err != nil {
                log.Error("Failed to process session", "error", err)
            }
        }
    }
}

func (w *SessionWorker) processNext(ctx context.Context) error {
    // Claim next pending session
    session, err := w.sessionService.ClaimNextPendingSession(ctx, w.podID)
    if err != nil {
        return err
    }
    if session == nil {
        return nil // No work
    }
    
    // Process session (orchestration logic)
    // ...
    
    return nil
}
```

**Orphan Detection:**
```go
func (s *SessionService) FindOrphanedSessions(
    ctx context.Context,
    timeoutThreshold time.Time,
) ([]*ent.AlertSession, error) {
    return s.client.AlertSession.Query().
        Where(
            alertsession.StatusEQ(alertsession.StatusInProgress),
            alertsession.LastInteractionAtNotNil(),
            alertsession.LastInteractionAtLT(timeoutThreshold),
        ).
        All(ctx)
}
```

**🔄 CHANGES FROM OLD TARSY:**
- ✅ **KEEP**: Same queue design (proven effective)
- ➕ **IMPROVE**: Type-safe Ent queries
- ➕ **ADD**: Better context cancellation support

---

## Connection Pooling Configuration

### Recommended Settings

**Development:**
```go
Config{
    MaxOpenConns:    10,
    MaxIdleConns:    5,
    ConnMaxLifetime: 30 * time.Minute,
    ConnMaxIdleTime: 5 * time.Minute,
}
```

**Production:**
```go
Config{
    MaxOpenConns:    25,  // Per pod
    MaxIdleConns:    10,
    ConnMaxLifetime: 1 * time.Hour,
    ConnMaxIdleTime: 15 * time.Minute,
}
```

**Considerations:**
- PostgreSQL `max_connections` = 100 (default)
- If 3 pods: 25 * 3 = 75 connections max
- Leave headroom for admin connections
- Monitor with `pg_stat_activity`

**🔄 CHANGES FROM OLD TARSY:**
- ✅ **KEEP**: Same pooling strategy
- ➕ **IMPROVE**: Go-native connection management

---

## Local Development Setup

### Podman Compose Configuration

**File: `deploy/podman-compose.yml`**
```yaml
version: '3.8'

services:
  postgres:
    image: postgres:16-alpine
    container_name: tarsy-postgres
    environment:
      POSTGRES_USER: tarsy
      POSTGRES_PASSWORD: tarsy_dev_password
      POSTGRES_DB: tarsy
    ports:
      - "5432:5432"
    volumes:
      - postgres_data:/var/lib/postgresql/data
      - ./postgres-init:/docker-entrypoint-initdb.d
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U tarsy"]
      interval: 5s
      timeout: 5s
      retries: 5

volumes:
  postgres_data:
```

**File: `deploy/postgres-init/01-init.sql`**
```sql
-- Enable extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Create application user (if needed for production)
-- Already created by POSTGRES_USER for dev

-- Performance tuning for development
ALTER SYSTEM SET shared_buffers = '256MB';
ALTER SYSTEM SET effective_cache_size = '1GB';
ALTER SYSTEM SET maintenance_work_mem = '64MB';
ALTER SYSTEM SET checkpoint_completion_target = 0.9;
ALTER SYSTEM SET wal_buffers = '16MB';
ALTER SYSTEM SET default_statistics_target = 100;
ALTER SYSTEM SET random_page_cost = 1.1;
ALTER SYSTEM SET effective_io_concurrency = 200;
```

**Development Workflow:**
```bash
# Start DB
podman-compose -f deploy/podman-compose.yml up -d

# Run Go service on host
export DATABASE_URL="postgres://tarsy:tarsy_dev_password@localhost:5432/tarsy?sslmode=disable"
go run cmd/tarsy/main.go

# Stop DB
podman-compose -f deploy/podman-compose.yml down
```

**Benefits:**
- Fast iteration (no container rebuilds for Go service)
- Easy debugging (native debugger support)
- Hot reload with air/reflex
- Full IDE integration

**🔄 CHANGES FROM OLD TARSY:**
- ➕ **IMPROVE**: Podman instead of Docker (rootless, daemonless)
- ➕ **IMPROVE**: Services on host (faster dev cycle)
- ✅ **KEEP**: Same database-in-container approach

---

## Retention Policy & Soft Deletes

### Soft Delete Strategy

**Retention Policy:**
1. **Day 0-90**: Active sessions (visible in dashboard)
2. **Day 90+**: Soft-deleted (hidden, but restorable if needed)

**Future Extension:**
Hard delete can be added later without schema changes (deleted_at field supports both).

### Implementation

```go
// Soft delete via retention policy (e.g., sessions older than 90 days)
func softDeleteOldSessions(ctx context.Context, client *ent.Client) error {
    cutoff := time.Now().Add(-90 * 24 * time.Hour)
    
    updated, err := client.AlertSession.
        Update().
        Where(
            alertsession.CompletedAtLT(cutoff),
            alertsession.DeletedAtIsNil(), // Only non-deleted
        ).
        SetDeletedAt(time.Now()).
        Save(ctx)
    
    log.Printf("Soft deleted %d sessions older than 90 days", updated)
    return err
}

// Default queries exclude soft-deleted
func (r *Repository) GetActiveSessions(ctx context.Context) ([]*ent.AlertSession, error) {
    return r.client.AlertSession.
        Query().
        Where(alertsession.DeletedAtIsNil()). // Exclude soft-deleted
        All(ctx)
}

// Restore if needed
func (r *Repository) RestoreSession(ctx context.Context, sessionID string) error {
    return r.client.AlertSession.
        UpdateOneID(sessionID).
        ClearDeletedAt().
        Exec(ctx)
}
```

**Scheduled Cleanup Job:**
```go
// Run daily via cron
func runRetentionPolicy(ctx context.Context, client *ent.Client) {
    if err := softDeleteOldSessions(ctx, client); err != nil {
        log.Printf("Retention policy failed: %v", err)
    }
}
```

### Benefits

- ✅ **Safety net**: Can restore accidentally removed sessions
- ✅ **Simple**: One cleanup job (not two)
- ✅ **Flexible schema**: Can add hard delete later without schema changes
- ✅ **Simple queries**: Just add `WHERE deleted_at IS NULL` for active data
- ✅ **Ent support**: Native Ent mixin for soft deletes

**Index:**
```sql
CREATE INDEX idx_alert_sessions_deleted_at ON alert_sessions(deleted_at) 
WHERE deleted_at IS NOT NULL;
```

**Note:** Child entities (stages, executions, events, etc.) remain in database with soft-deleted sessions. Hard delete feature can be added later if needed to permanently remove old data.

---

## Testing Strategy

### Test Database Setup

```go
// pkg/database/testing.go

func NewTestClient(t *testing.T) *ent.Client {
    // Use testcontainers for PostgreSQL
    ctx := context.Background()
    
    pgContainer, err := postgres.RunContainer(ctx,
        testcontainers.WithImage("postgres:16-alpine"),
        postgres.WithDatabase("test"),
        postgres.WithUsername("test"),
        postgres.WithPassword("test"),
    )
    require.NoError(t, err)
    
    t.Cleanup(func() {
        pgContainer.Terminate(ctx)
    })
    
    connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
    require.NoError(t, err)
    
    client, err := ent.Open("postgres", connStr)
    require.NoError(t, err)
    
    // Run migrations
    err = client.Schema.Create(ctx)
    require.NoError(t, err)
    
    t.Cleanup(func() {
        client.Close()
    })
    
    return client
}
```

### Test Structure

**Basic Service Tests:**
```go
func TestSessionService_CreateSession(t *testing.T) {
    client := NewTestClient(t)
    service := NewSessionService(client)
    
    ctx := context.Background()
    session, err := service.CreateSession(ctx, CreateSessionRequest{
        SessionID: "test-123",
        AlertData: "test alert",
        ChainID:   "k8s-analysis",
        // ...
    })
    
    require.NoError(t, err)
    assert.Equal(t, "test-123", session.SessionID)
    
    // Verify initial stage and agent execution created
    stages, err := client.Stage.Query().
        Where(stage.SessionIDEQ(session.SessionID)).
        All(ctx)
    require.NoError(t, err)
    assert.Len(t, stages, 1)
    
    executions, err := client.AgentExecution.Query().
        Where(agentexecution.StageIDEQ(stages[0].StageID)).
        All(ctx)
    require.NoError(t, err)
    assert.Len(t, executions, 1)
}
```

**Timeline Event Tests:**
```go
func TestTimelineService_CreateAndUpdate(t *testing.T) {
    client := NewTestClient(t)
    service := NewTimelineService(client)
    
    // Create streaming event
    event, err := service.CreateTimelineEvent(ctx, CreateTimelineEventRequest{
        SessionID:      sessionID,
        StageID:        stageID,
        ExecutionID:    executionID,
        SequenceNumber: 1,
        EventType:      timelineevent.EventTypeLLMThinking,
        Status:         timelineevent.StatusStreaming,
        Content:        "Analyzing...",
    })
    require.NoError(t, err)
    
    // Update with more content
    err = service.UpdateTimelineEvent(ctx, event.EventID, "Analyzing pods...")
    require.NoError(t, err)
    
    // Complete event
    err = service.CompleteTimelineEvent(ctx, event.EventID, "Analysis complete!")
    require.NoError(t, err)
    
    // Verify final state
    updated, err := client.TimelineEvent.Get(ctx, event.EventID)
    require.NoError(t, err)
    assert.Equal(t, timelineevent.StatusCompleted, updated.Status)
    assert.Equal(t, "Analysis complete!", updated.Content)
}
```

**Message & Context Building Tests:**
```go
func TestMessageService_ConversationBuilding(t *testing.T) {
    client := NewTestClient(t)
    service := NewMessageService(client)
    
    // Create messages
    msg1, _ := service.CreateMessage(ctx, CreateMessageRequest{
        ExecutionID: executionID,
        Role:        message.RoleSystem,
        Content:     "You are KubernetesAgent...",
    })
    
    msg2, _ := service.CreateMessage(ctx, CreateMessageRequest{
        ExecutionID: executionID,
        Role:        message.RoleUser,
        Content:     "Analyze pods...",
    })
    
    msg3, _ := service.CreateMessage(ctx, CreateMessageRequest{
        ExecutionID: executionID,
        Role:        message.RoleAssistant,
        Content:     "I found 10 pods...",
    })
    
    // Get conversation
    messages, err := service.GetExecutionMessages(ctx, executionID)
    require.NoError(t, err)
    assert.Len(t, messages, 3)
    assert.Equal(t, msg1.MessageID, messages[0].MessageID)
    
    // Test lazy context building
    agent := NewKubernetesAgent(client)
    context, err := agent.BuildStageContext(ctx, stageID)
    require.NoError(t, err)
    assert.Contains(t, context, "I found 10 pods")
}
```

**Parallel Agent Tests:**
```go
func TestStageService_ParallelAgents(t *testing.T) {
    client := NewTestClient(t)
    service := NewStageService(client)
    
    // Create stage with 3 parallel agents
    stage, err := service.CreateStage(ctx, CreateStageRequest{
        SessionID:           sessionID,
        StageName:          "Deep Dive",
        StageIndex:         1,
        ExpectedAgentCount: 3,
        ParallelType:       stage.ParallelTypeMultiAgent,
        SuccessPolicy:      stage.SuccessPolicyAll,
    })
    require.NoError(t, err)
    
    // Create agent executions
    for i := 1; i <= 3; i++ {
        _, err := service.CreateAgentExecution(ctx, CreateAgentExecutionRequest{
            StageID:    stage.StageID,
            AgentName:  fmt.Sprintf("Agent%d", i),
            AgentIndex: i,
        })
        require.NoError(t, err)
    }
    
    // Complete all agents
    executions, _ := client.AgentExecution.Query().
        Where(agentexecution.StageIDEQ(stage.StageID)).
        All(ctx)
    
    for _, exec := range executions {
        exec.Update().
            SetStatus(agentexecution.StatusCompleted).
            SetCompletedAt(time.Now()).
            Save(ctx)
    }
    
    // Aggregate stage status
    err = service.AggregateStageStatus(ctx, stage.StageID)
    require.NoError(t, err)
    
    // Verify stage completed
    updated, _ := client.Stage.Get(ctx, stage.StageID)
    assert.Equal(t, stage.StatusCompleted, updated.Status)
}
```

**Full-Text Search Tests:**
```go
func TestFullTextSearch(t *testing.T) {
    client := NewTestClient(t)
    
    // Create sessions with searchable content
    session1, _ := client.AlertSession.Create().
        SetAlertData("Critical error in production cluster").
        Save(ctx)
    
    session2, _ := client.AlertSession.Create().
        SetAlertData("Warning: high memory usage").
        Save(ctx)
    
    // Search with full-text query
    results, err := client.AlertSession.Query().
        Where(func(s *sql.Selector) {
            s.Where(sql.P(func(b *sql.Builder) {
                b.WriteString("to_tsvector('english', alert_data) @@ to_tsquery(?)")
                b.Arg("error & production")
            }))
        }).
        All(ctx)
    
    require.NoError(t, err)
    assert.Len(t, results, 1)
    assert.Equal(t, session1.SessionID, results[0].SessionID)
}
```

**Soft Delete Tests:**
```go
func TestSoftDelete(t *testing.T) {
    client := NewTestClient(t)
    service := NewSessionService(client)
    
    // Create and complete session
    session, _ := service.CreateSession(ctx, req)
    session.Update().
        SetStatus(alertsession.StatusCompleted).
        SetCompletedAt(time.Now().Add(-100 * 24 * time.Hour)). // 100 days ago
        Save(ctx)
    
    // Soft delete old sessions
    err := softDeleteOldSessions(ctx, client)
    require.NoError(t, err)
    
    // Verify session is soft-deleted
    updated, _ := client.AlertSession.Get(ctx, session.SessionID)
    assert.NotNil(t, updated.DeletedAt)
    
    // Default query excludes soft-deleted
    active, _ := service.GetActiveSessions(ctx)
    assert.Len(t, active, 0)
    
    // Restore session
    err = service.RestoreSession(ctx, session.SessionID)
    require.NoError(t, err)
    
    // Verify restored
    restored, _ := client.AlertSession.Get(ctx, session.SessionID)
    assert.Nil(t, restored.DeletedAt)
}
```

**🔄 CHANGES FROM OLD TARSY:**
- ➕ **IMPROVE**: testcontainers for isolation
- ➕ **IMPROVE**: No SQLite test mode (test against real DB)
- ➕ **ADD**: Tests for new five-layer architecture
- ➕ **ADD**: Tests for lazy context building
- ➕ **ADD**: Tests for parallel agent execution
- ➕ **ADD**: Tests for full-text search
- ➕ **ADD**: Tests for soft delete retention

---

## Performance Considerations

### Indexing Strategy

**Critical Indexes:**
1. `AlertSession(status, started_at)` - Queue queries
2. `AlertSession(status, last_interaction_at)` - Orphan detection
3. `AlertSession(deleted_at)` WHERE deleted_at IS NOT NULL - Soft delete queries
4. `Stage(session_id, stage_index)` - Unique, stage ordering
5. `AgentExecution(stage_id, agent_index)` - Unique, agent ordering
6. `AgentExecution(session_id)` - Session-wide queries
7. `TimelineEvent(session_id, sequence_number)` - Timeline ordering
8. `TimelineEvent(stage_id, sequence_number)` - Stage timeline grouping
9. `TimelineEvent(execution_id, sequence_number)` - Agent timeline filtering
10. `Message(execution_id, sequence_number)` - Agent conversation order
11. `LLMInteraction(execution_id, created_at)` - Agent's LLM calls chronologically
12. `MCPInteraction(execution_id, created_at)` - Agent's MCP calls chronologically
13. `Event(session_id)` - Session-based cleanup
14. `Event(channel, id)` - Polling queries

**Full-Text Search Indexes (GIN):**
1. `to_tsvector('english', alert_data)` - Search alert content
2. `to_tsvector('english', final_analysis)` - Search investigation summaries

### Query Optimization

**Use Ent's Query Optimizations:**
```go
// Eager loading with edges - NEW SCHEMA
sessions, err := client.AlertSession.Query().
    WithStages(func(q *ent.StageQuery) {
        q.WithAgentExecutions(func(aq *ent.AgentExecutionQuery) {
            aq.WithLLMInteractions()
            aq.WithMCPInteractions()
        })
    }).
    All(ctx)

// Limit fields for list views
sessions, err := client.AlertSession.Query().
    Select(
        alertsession.FieldSessionID,
        alertsession.FieldStatus,
        alertsession.FieldStartedAt,
    ).
    All(ctx)

// Pagination
sessions, err := client.AlertSession.Query().
    Limit(20).
    Offset(page * 20).
    All(ctx)

// Timeline for entire session
events, err := client.TimelineEvent.Query().
    Where(timelineevent.SessionIDEQ(sessionID)).
    Order(ent.Asc(timelineevent.FieldSequenceNumber)).
    All(ctx)

// Timeline for a stage (all agents combined)
events, err := client.TimelineEvent.Query().
    Where(timelineevent.StageIDEQ(stageID)).
    Order(ent.Asc(timelineevent.FieldSequenceNumber)).
    All(ctx)

// Timeline for specific agent in parallel stage
events, err := client.TimelineEvent.Query().
    Where(timelineevent.ExecutionIDEQ(executionID)).
    Order(ent.Asc(timelineevent.FieldSequenceNumber)).
    All(ctx)

// Messages for agent's conversation
messages, err := client.Message.Query().
    Where(message.ExecutionIDEQ(executionID)).
    Order(ent.Asc(message.FieldSequenceNumber)).
    All(ctx)

// All stages in a session
stages, err := client.Stage.Query().
    Where(stage.SessionIDEQ(sessionID)).
    Order(ent.Asc(stage.FieldStageIndex)).
    All(ctx)

// All agent executions for a stage
executions, err := client.AgentExecution.Query().
    Where(agentexecution.StageIDEQ(stageID)).
    Order(ent.Asc(agentexecution.FieldAgentIndex)).
    All(ctx)
```

### Full-Text Search Queries

**PostgreSQL full-text search with GIN indexes:**
```go
// Search alerts by content
sessions, err := client.AlertSession.Query().
    Where(func(s *sql.Selector) {
        s.Where(sql.P(func(b *sql.Builder) {
            b.WriteString("to_tsvector('english', alert_data) @@ to_tsquery(?)")
            b.Arg("error & critical")
        }))
    }).
    All(ctx)

// Search final analysis
sessions, err := client.AlertSession.Query().
    Where(func(s *sql.Selector) {
        s.Where(sql.P(func(b *sql.Builder) {
            b.WriteString("to_tsvector('english', final_analysis) @@ to_tsquery(?)")
            b.Arg("memory | cpu")
        }))
    }).
    All(ctx)

// Full-text search with ranking
type SessionWithRank struct {
    *ent.AlertSession
    Rank float64
}

var results []SessionWithRank
err := client.AlertSession.Query().
    Modify(func(s *sql.Selector) {
        s.Select(
            s.C("*"),
            sql.As(sql.ExprP("ts_rank(to_tsvector('english', alert_data), to_tsquery($1))", "error"), "rank"),
        ).
        Where(sql.P(func(b *sql.Builder) {
            b.WriteString("to_tsvector('english', alert_data) @@ to_tsquery($1)")
        })).
        OrderBy(sql.Desc("rank"))
    }).
    Scan(ctx, &results)
```

### Soft Delete Queries

**Working with soft deletes:**
```go
// Get only active (non-deleted) sessions (default query)
sessions, err := client.AlertSession.Query().
    Where(alertsession.DeletedAtIsNil()).
    All(ctx)

// Soft delete old sessions (retention policy)
cutoff := time.Now().Add(-90 * 24 * time.Hour)
updated, err := client.AlertSession.
    Update().
    Where(
        alertsession.CompletedAtLT(cutoff),
        alertsession.DeletedAtIsNil(),
    ).
    SetDeletedAt(time.Now()).
    Save(ctx)

// Restore soft-deleted session
err := client.AlertSession.
    UpdateOneID(sessionID).
    ClearDeletedAt().
    Exec(ctx)
```

**🔄 CHANGES FROM OLD TARSY:**
- ➕ **IMPROVE**: Ent's eager loading (N+1 query prevention)
- ➕ **IMPROVE**: Better type safety in queries
- ➕ **ADD**: Full-text search support with GIN indexes
- ➕ **ADD**: Soft delete patterns
- ➕ **ADD**: New entity queries (Stage, AgentExecution, TimelineEvent, Message)

---

## Implementation Checklist

### Phase 2.1: Schema & Migrations
- [x] Define Ent schemas for all entities
  - [x] AlertSession (with full-text search, soft delete)
  - [x] Stage (Layer 0a - Configuration + Coordination)
  - [x] AgentExecution (Layer 0b - Individual Agent Work)
  - [x] TimelineEvent (Layer 1 - UX Timeline)
  - [x] Message (Layer 2 - LLM Conversation)
  - [x] LLMInteraction (Layer 3 - Debug)
  - [x] MCPInteraction (Layer 4 - Debug)
  - [x] Event (with session_id for cleanup)
  - [x] Chat (without conversation_history)
  - [x] ChatUserMessage
- [x] Configure enum types
- [x] Set up edges/relationships
- [x] Generate initial migration
- [x] Test migration on fresh database
- [x] Document schema in generated docs

### Phase 2.2: Database Client
- [x] Implement database client initialization
- [x] Add connection pool configuration
- [x] Implement migration runner
- [x] Add health check endpoint
- [x] Set up connection metrics
- [x] Configure GIN indexes for full-text search

### Phase 2.3: Service Layer
- [x] Implement `SessionService`
  - [x] CreateSession (with initial stage + agent execution)
  - [x] GetSession
  - [x] UpdateSession
  - [x] ClaimNextPendingSession
  - [x] FindOrphanedSessions
  - [x] SoftDeleteOldSessions (retention policy)
- [x] Implement `StageService`
  - [x] CreateStage (with agent executions)
  - [x] UpdateStageStatus (aggregated from agents)
  - [x] GetStages
  - [x] GetAgentExecutions
- [x] Implement `TimelineService`
  - [x] CreateTimelineEvent
  - [x] UpdateTimelineEvent (streaming support)
  - [x] GetSessionTimeline
  - [x] GetStageTimeline
  - [x] GetAgentTimeline
- [x] Implement `MessageService`
  - [x] CreateMessage
  - [x] GetExecutionMessages (for LLM context)
  - [x] GetStageMessages
- [x] Implement `InteractionService`
  - [x] CreateLLMInteraction (with last_message_id)
  - [x] CreateMCPInteraction
  - [x] GetLLMInteractions (list + detail views)
  - [x] GetMCPInteractions (list + detail views)
  - [x] ReconstructConversation (from Message table)
- [x] Implement `ChatService`
  - [x] CreateChat (with lazy context building)
  - [x] AddChatMessage
  - [x] GetChatHistory
  - [x] BuildChatContext (lazy evaluation)
- [x] Implement `EventService`
  - [x] CreateEvent
  - [x] GetEvents (polling)
  - [x] CleanupSessionEvents (on session completion)
  - [x] CleanupOrphanedEvents (TTL fallback)

### Phase 2.4: Testing
- [ ] Set up testcontainers integration
- [ ] Write service layer unit tests
- [ ] Write integration tests
- [ ] Test migration rollback scenarios
- [ ] Test full-text search functionality
- [ ] Test soft delete retention policies
- [ ] Test event cleanup strategies

---

## Design Decisions

**Chain Registry**: Chain definitions stored in `agents.yaml` configuration file (same as old TARSy). Loaded at startup into in-memory registry. Chain modifications require redeployment. Benefits: version controlled, simple, code review process, no runtime dependencies.

---

## Decided Against

**Read Replicas**: Not needed. Expected load is low (few parallel sessions, ~50 iterations each, few minutes duration). Real-time streaming requirements make replication lag problematic. Single PostgreSQL instance is sufficient.

**Table Partitioning**: Not needed. Expected volume (~3-4K sessions/year) is very low. Soft delete retention policy (90 days) keeps active data small. Can revisit if table exceeds 1M rows or queries become slow.

---

## Summary of Key Changes from Old TARSy

### 🆕 New Entities

1. **Stage (Layer 0a)**: Chain stage configuration + coordination
   - Replaces old "StageExecution" concept with cleaner separation
   - Aggregates status from agent executions
   - No output field - lazy context building instead

2. **AgentExecution (Layer 0b)**: Individual agent work
   - Each stage has 1+ agent executions (uniform model)
   - Enables clean parallel agent support
   - No output field - lazy context building instead

3. **TimelineEvent (Layer 1)**: UX-focused reasoning timeline
   - Eliminates frontend de-duplication problem
   - event_id tracking for streaming updates
   - Separate from debug data

4. **Message (Layer 2)**: LLM conversation context
   - Eliminates O(n²) conversation duplication
   - Linear storage: each message stored once
   - Execution-scoped conversations

### ⚠️ Modified Entities

1. **AlertSession**:
   - ➕ `deleted_at` for soft delete retention policy
   - ➕ Full-text search on `alert_data` and `final_analysis`
   - ➖ Remove `chain_definition` (use live lookup from registry)
   - ➖ Remove `pause_metadata` (pause feature dropped)
   - ⚠️ `alert_data` is TEXT not JSON

2. **LLMInteraction (Layer 3)**:
   - ➖ Remove `conversation` field
   - ➕ Add `last_message_id` for conversation reconstruction
   - Link to Stage + AgentExecution (not old StageExecution)

3. **MCPInteraction (Layer 4)**:
   - Link to Stage + AgentExecution (not old StageExecution)
   - Simplified interaction types

4. **Event**:
   - ➕ Add `session_id` for targeted cleanup
   - Automatic cleanup on session completion

5. **Chat**:
   - ➖ Remove `conversation_history` (lazy context building)
   - ➖ Remove `context_captured_at`

### ❌ Removed Features

1. **Pause/Resume**: Dropped for simplicity (use forced_conclusion instead)
2. **Chain Definition Snapshot**: Use live lookup from registry
3. **Stage/Agent Output Fields**: Lazy context building instead
4. **Conversation Field in LLMInteraction**: Use Message table + last_message_id

### ✅ New Features

1. **Lazy Context Building**: 
   - Agent.BuildStageContext() method
   - Context generated on-demand when needed
   - Works with single and parallel agents
   - No wasted computation

2. **Full-Text Search**:
   - PostgreSQL GIN indexes
   - Search alert_data and final_analysis
   - Boolean operators, stemming, ranking

3. **Soft Delete Retention Policy**:
   - Soft delete for sessions older than 90 days
   - Restorable if needed
   - Schema supports hard delete (can be added later)

4. **Event Cleanup Strategy**:
   - Automatic cleanup on session completion
   - TTL fallback for orphaned events
   - Minimal storage footprint

5. **Stage Status Aggregation**:
   - Aggregated from agent execution statuses
   - Support for success_policy (all/any)
   - Handles parallel agents cleanly

6. **Separate Timeline & Debug Pages**:
   - Main page: TimelineEvents only (fast)
   - Debug page: LLM/MCP interactions (lazy loaded)
   - Better performance, cleaner separation

### 🎯 Benefits

**Performance:**
- ✅ O(n) message storage (not O(n²))
- ✅ No frontend de-duplication logic
- ✅ Fast main page load (no debug data)
- ✅ Lazy loading everywhere (context, debug details)

**Architecture:**
- ✅ Clean separation of concerns (5 layers)
- ✅ Uniform stage model (single/parallel)
- ✅ Proper separation of UX and debug data
- ✅ Better indexing strategy

**Developer Experience:**
- ✅ Type-safe Ent queries
- ✅ Go-native patterns throughout
- ✅ Clear service layer boundaries
- ✅ Easier to test (testcontainers)

**Operations:**
- ✅ Automatic cleanup strategies
- ✅ Soft delete safety net
- ✅ Full-text search for analytics
- ✅ Better observability (separate debug page)

---

## Next Steps

After approval of this design:

1. ✅ Review completed questions document (`phase2-database-schema-questions.md`)
2. Create Ent schema definitions for all entities
   - AlertSession with full-text search, soft delete
   - Stage + AgentExecution hierarchy
   - TimelineEvent, Message, LLMInteraction, MCPInteraction
   - Event, Chat, ChatUserMessage
3. Generate initial migration with Atlas
4. Set up podman-compose for local dev
5. Implement core service layer
   - SessionService, StageService, TimelineService
   - MessageService, InteractionService, ChatService, EventService
6. Implement lazy context building pattern
   - Agent interface with BuildStageContext method
   - Context builders for each agent type
7. Write comprehensive tests
   - Service layer tests
   - Integration tests
   - Full-text search tests
   - Soft delete tests
   - Parallel agent tests
8. Integrate with Phase 1 POC code
9. Set up cleanup jobs (event cleanup, soft delete retention policy)
10. Document API endpoints for timeline and debug pages

---

## References

- [Ent Documentation](https://entgo.io/docs/getting-started)
- [Atlas Migrations](https://atlasgo.io/)
- [PostgreSQL Connection Pooling Best Practices](https://www.postgresql.org/docs/current/runtime-config-connection.html)
- [PostgreSQL Full-Text Search](https://www.postgresql.org/docs/current/textsearch.html)
- Old TARSy Database Implementation: `/home/igels/Projects/AI/tarsy-bot/backend/tarsy/`
- Design Questions Document: `docs/phase2-database-schema-questions.md` (completed)
