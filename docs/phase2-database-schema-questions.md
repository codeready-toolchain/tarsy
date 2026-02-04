# Phase 2: Database Schema - Design Questions

This document contains questions and concerns about the proposed database schema that need discussion before finalizing the design.

**Status**: 🟡 Pending Discussion  
**Created**: 2026-02-03  
**Purpose**: Identify improvements over old TARSy schema based on lessons learned

---

## How to Use This Document

For each question:
1. ✅ = Decided
2. 🔄 = In Discussion  
3. ⏸️ = Deferred
4. ❌ = Rejected

Add your answers inline under each question, then we'll update the main design doc.

---

## 🔥 Critical Priority (Storage/Performance)

### Q1: Database Schema Architecture - Multi-Layer Model

**Status**: ✅ **DECIDED**

**Original Problems:**
1. Each `LLMInteraction` stores full `conversation` JSON field → O(n²) storage growth
2. Frontend receives streamed chunks + DB records → Must de-duplicate same content
3. Reasoning tab (UX) and Debug tab (observability) data mixed in same entities
4. Stage vs Agent Execution conflated in single `StageExecution` table

**Decided Solution: Five-Layer Architecture**

---

## Core Hierarchy: Stage + AgentExecution

### Why Two Tables?

**Conceptual Model:**
```
Session
  ├─ Stage 1: "Initial Analysis" (single agent)
  │   └─ AgentExecution 1 (KubernetesAgent)
  │
  ├─ Stage 2: "Deep Dive" (3 parallel agents)
  │   ├─ AgentExecution 1 (KubernetesAgent)
  │   ├─ AgentExecution 2 (ArgoCDAgent)
  │   └─ AgentExecution 3 (PrometheusAgent)
  │
  └─ Stage 3: "Recommendations" (single agent)
      └─ AgentExecution 1 (KubernetesAgent)
```

**Key Insight:** Every stage has 1+ agent executions. No special cases for single vs parallel.

---

### Layer 0a: Stage (Chain Stage - Configuration + Coordination)

**Purpose:** Represents a stage in the processing chain, coordinates agent execution(s)

```
Stage:
// Identity
- stage_id              string    PK, UUID
- session_id            string    FK → AlertSession (indexed)

// Stage Configuration
- stage_name            string    "Initial Analysis", "Deep Dive", etc.
- stage_index           int       Position in chain: 0, 1, 2... (indexed)

// Execution Mode
- expected_agent_count  int       How many agents (1 for single, N for parallel)
- parallel_type         *enum     null if count=1, "multi_agent"/"replica" if count>1
- success_policy        *enum     null if count=1, "all"/"any" if count>1

// Stage-Level Status & Timing (aggregated from agent executions)
- status                enum      pending, active, completed, failed, timed_out, cancelled
- started_at            *time.Time  When first agent started
- completed_at          *time.Time  When stage finished (any terminal state)
- duration_ms           *int        Total stage duration
- error_message         *string   Aggregated error if stage failed/timed_out/cancelled

// Chat Context (if applicable)
- chat_id               *string   FK → Chat
- chat_user_message_id  *string   FK → ChatUserMessage

Indexes:
- (session_id, stage_index) - Unique, stage ordering within session
- stage_id - Primary lookups
```

**Stage Status Aggregation Logic:**

**Key Rule:** Stage remains `active` while ANY agent is `pending` or `active`. Stage status is only determined when ALL agents have terminated.

**Agent Statuses:**
- `pending`: Not yet started (initial state)
- `active`: Currently executing
- `completed`: Finished successfully
- `failed`: Failed with error
- `timed_out`: Exceeded timeout limit
- `cancelled`: Manually cancelled

**Terminal States:** `completed`, `failed`, `timed_out`, `cancelled`

**Aggregation Rules (when all agents terminated):**

**For `success_policy = "all"`** (all agents must succeed):
1. If ALL agents `completed` → Stage `completed`
2. Otherwise:
   - If ALL agents `timed_out` → Stage `timed_out`
   - If ALL agents `cancelled` → Stage `cancelled`
   - Mixed failures → We will define the logic of picking the overall session status later.

**For `success_policy = "any"`** (at least one agent must succeed):
1. If ANY agent `completed` → Stage `completed` (even if others failed/timed_out/cancelled)
2. Otherwise (all failed):
   - If ALL agents `timed_out` → Stage `timed_out`
   - If ALL agents `cancelled` → Stage `cancelled`
   - If at least one agent `completed` → Stage `completed`
   - Mixed failures (no agent `completed`) → We will define the logic of picking the overall session status later.

**Stage stays `active`** while ANY agent is `pending` or `active`

---

### Layer 0b: AgentExecution (Individual Agent Work)

**Purpose:** Each stage has 1+ agent executions. This is where the actual work happens.

```
AgentExecution:
// Identity
- execution_id          string    PK, UUID
- stage_id              string    FK → Stage (indexed)
- session_id            string    FK → AlertSession (indexed)

// Agent Details
- agent_name            string    "KubernetesAgent", "ArgoCDAgent", etc.
- agent_index           int       1 for single, 1-N for parallel (indexed)

// Execution Status & Timing
- status                enum      pending, active, completed, failed, cancelled, timed_out
- started_at            *time.Time
- completed_at          *time.Time
- duration_ms           *int
- error_message         *string   Error details if failed

// Agent Configuration
- iteration_strategy    string    "react", "native_thinking", etc. (for observability)

Indexes:
- (stage_id, agent_index) - Unique, agent ordering within stage
- execution_id - Primary lookups
- session_id - Session-wide queries
```

**Why both stage_id and session_id?**
- `stage_id`: Required for stage-scoped queries (get all executions for a stage)
- `session_id`: Optimization for session-wide queries (avoid joins)

---

## Context Building Pattern (Lazy Evaluation)

**Design Decision:** No `stage_output` or `agent_output` fields in the database!

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

**When stage has multiple parallel agents, aggregate all their outputs:**

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

**Key Points for Parallel Agents:**
- Query all `AgentExecution` records for the stage
- Loop through each execution and extract its artifacts
- Aggregate/synthesize into unified context
- Each agent formats its own data appropriately

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

### Future: Optional Caching

If performance becomes a concern, cache generated contexts:

```go
// Optional: Cache formatted context
cacheKey := fmt.Sprintf("stage_context:%s", stageID)
if cached := cache.Get(cacheKey); cached != "" {
    return cached, nil
}
context, _ := agent.BuildStageContext(ctx, stageID)
cache.Set(cacheKey, context, 1*time.Hour)
return context, nil
```

---

### Layer 1: TimelineEvent (Reasoning Tab - UX-focused)

**Purpose:** User-facing investigation timeline, streamed in real-time

```
TimelineEvent:
// Identity & Hierarchy
- event_id            string    PK
- session_id          string    FK → AlertSession (indexed)
- stage_id            string    FK → Stage (indexed) - Stage grouping
- execution_id        string    FK → AgentExecution (indexed) - Which agent

// Timeline Ordering
- sequence_number     int       Order in timeline

// Timestamps
- created_at          time.Time Creation timestamp
- updated_at          time.Time Last update (for streaming)

// Event Details
- event_type          enum      llm_thinking, llm_response, llm_tool_call,
                                mcp_tool_call, mcp_tool_summary,
                                user_question, executive_summary, final_analysis
- status              enum      streaming, completed, failed, cancelled, timed_out
- content             string    Event content (grows during streaming, updateable on completion)
- metadata            JSON      Type-specific data (tool_name, server_name, etc.)

// Debug Links
- llm_interaction_id  *string   Link to debug details (set on completion)
- mcp_interaction_id  *string   Link to debug details (set on completion)

Indexes:
- (session_id, sequence_number) - Timeline ordering
- (stage_id, sequence_number) - Stage timeline grouping
- (execution_id, sequence_number) - Agent timeline filtering
- event_id - Updates by ID
- created_at - Chronological queries
```

**Why both stage_id AND execution_id?**

**Reasoning Tab - Group by stage:**
```go
// Show all events for Stage 2 (all 3 parallel agents combined)
events := client.TimelineEvent.Query().
    Where(timelineevent.StageIDEQ(stageID)).
    Order(ent.Asc(timelineevent.FieldSequenceNumber)).
    All(ctx)
```

**Reasoning Tab - Filter by specific agent in parallel stage:**
```go
// Show only Agent 2's events in Stage 2
events := client.TimelineEvent.Query().
    Where(timelineevent.ExecutionIDEQ(executionID)).
    Order(ent.Asc(timelineevent.FieldSequenceNumber)).
    All(ctx)
```

**Key Features:**
- ✅ **Created IMMEDIATELY** when streaming starts (not after completion)
- ✅ **Updateable** during streaming, immutable after completion
- ✅ **Frontend uses event_id** to track updates - NO de-duplication logic needed!
- ✅ **Single writer** per event (the agent generating it)

**Streaming Flow:**
```
1. Start streaming → Create TimelineEvent with status='streaming'
2. During streaming → Update content field, stream chunks with event_id
3. Streaming complete → Update status='completed'
Frontend: Updates same event_id throughout - no de-duplication! ✓
```

---

### Layer 2: Message (LLM Context)

**Purpose:** Conversation history for LLM API calls

```
Message:
// Identity & Hierarchy
- message_id          string    PK
- session_id          string    FK → AlertSession (indexed)
- stage_id            string    FK → Stage (indexed) - Stage scoping
- execution_id        string    FK → AgentExecution (indexed) - Agent conversation

// Message Details
- sequence_number     int       Execution-scoped order
- role                enum      system, user, assistant
- content             string    Message text
- created_at          time.Time Indexed

Indexes:
- (execution_id, sequence_number) - Agent conversation order
- (stage_id, execution_id) - Stage + agent scoping
```

**Why execution_id (not stage_id) for conversation?**

Each agent in a parallel stage has its **own separate conversation**:

```
Stage 2: Deep Dive (3 parallel agents)
  ├─ AgentExecution 1
  │   ├─ Message 1: "system: You are KubernetesAgent..."
  │   ├─ Message 2: "user: Analyze pods..."
  │   └─ Message 3: "assistant: I found..."
  │
  ├─ AgentExecution 2
  │   ├─ Message 1: "system: You are ArgoCDAgent..."
  │   ├─ Message 2: "user: Analyze applications..."
  │   └─ Message 3: "assistant: I found..."
  │
  └─ AgentExecution 3
      └─ (separate conversation)
```

**Key Features:**
- ✅ **Execution-scoped**: Each agent has its own conversation
- ✅ **Stage-scoped reset**: Each stage starts with fresh context
- ✅ **Immutable**: Messages never updated once created
- ✅ **Linear storage**: O(n) not O(n²) - no duplication!

**Usage:**
```go
// Get conversation for specific agent execution
messages := service.GetMessagesForExecution(ctx, executionID)

// Build LLM API request
conversation := buildLLMConversation(messages)

// Call LLM
response := llmClient.Call(ctx, conversation)

// Store new assistant message
service.CreateMessage(ctx, Message{
    ExecutionID: executionID,
    Role: "assistant",
    Content: response.Text,
})
```

---

### Layer 3: LLMInteraction (Debug Tab - Observability)

**Purpose:** Full technical details for LLM calls (debugging/analysis)

```
LLMInteraction:
// Identity & Hierarchy
- interaction_id      string    PK
- session_id          string    FK → AlertSession (indexed)
- stage_id            string    FK → Stage (indexed)
- execution_id        string    FK → AgentExecution (indexed) - Which agent

// Timing
- created_at          time.Time Indexed

// Interaction Details
- interaction_type    enum      iteration, final_analysis, executive_summary, chat_response
- model_name          string    "gemini-2.0-flash-thinking-exp", etc.

// Conversation Context (links to Message table)
- REMOVED: conversation field (use Message table instead)
+ last_message_id     *string   FK → Message (last message sent to LLM)

// Full API Details
- llm_request         JSON      Full API request payload
- llm_response        JSON      Full API response payload
- thinking_content    *string   Native thinking (Gemini)
- response_metadata   JSON      Grounding, tool usage, etc.

// Metrics & Result
- input_tokens        *int
- output_tokens       *int
- total_tokens        *int
- duration_ms         *int
- error_message       *string   null = success, not-null = failed

Indexes:
- (execution_id, created_at) - Agent's LLM calls chronologically
- (stage_id, created_at) - Stage's LLM calls
- interaction_id - Primary lookups
```

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

**Key Features:**
- ✅ **Created on completion** (not during streaming)
- ✅ **Immutable**: Full technical record for audit
- ✅ **Links to Messages**: Conversation reconstructed via `last_message_id`
- ✅ **Full API payloads**: Request/response for debugging
- ✅ **Success/Failure**: Determined by `error_message` (null = success, not-null = failed)

---

### Layer 4: MCPInteraction (Debug Tab - Observability)

**Purpose:** Full technical details for MCP tool calls (debugging/analysis)

```
MCPInteraction:
// Identity & Hierarchy
- interaction_id      string    PK
- session_id          string    FK → AlertSession (indexed)
- stage_id            string    FK → Stage (indexed)
- execution_id        string    FK → AgentExecution (indexed) - Which agent

// Timing
- created_at          time.Time Indexed

// Interaction Details
- interaction_type    enum      tool_call, tool_list
- server_name         string    "kubernetes", "argocd", etc.
- tool_name           *string   "kubectl_get_pods", etc.

// Full Details
- tool_arguments      JSON      Input parameters
- tool_result         JSON      Tool output
- available_tools     JSON      For tool_list type

// Result & Timing
- duration_ms         *int
- error_message       *string   null = success, not-null = failed

Indexes:
- (execution_id, created_at) - Agent's MCP calls chronologically
- (stage_id, created_at) - Stage's MCP calls
- interaction_id - Primary lookups
```

**Key Features:**
- ✅ **Created on completion** (not during streaming)
- ✅ **Immutable**: Full technical record for audit
- ✅ **Full API payloads**: Request/response for debugging
- ✅ **Success/Failure**: Determined by `error_message` (null = success, not-null = failed)

---

## Benefits of Five-Layer Architecture

### ✅ Clean Conceptual Model
```
Stage = Configuration + Coordination + Aggregated Results
AgentExecution = Individual Agent Work
TimelineEvent = UX Timeline
Message = LLM Conversation Context
LLMInteraction/MCPInteraction = Debug Details
```

### ✅ Uniform Stage Model
```
No special "parent execution" entities!
Every stage has 1+ agent executions (single or parallel treated uniformly)
```

### ✅ Solves O(n²) Storage Problem
```
Old: conversation field duplicates all messages in every iteration
     20 iterations = 420 messages stored (should be 40!)
New: Messages stored once in Message table
     20 iterations = 40 messages ✓
```

### ✅ Eliminates Frontend De-duplication
```
Old: Stream chunks → Store DB record → Frontend must de-duplicate ✗
New: Create TimelineEvent → Stream with event_id → Update same event ✓
     Frontend just updates existing event by ID!
```

### ✅ Separates Concerns
```
Reasoning Tab → Query TimelineEvents (fast, clean UX, stage/agent grouping)
Debug Tab     → Query LLMInteraction + MCPInteraction (full technical details)
LLM Context   → Query Messages for execution (conversation building)
Chain Logic   → Agent.BuildStageContext() generates context on-demand (lazy evaluation)
```

### ✅ Lazy Context Building
```
No stage_output or agent_output in database!
Context generated on-demand when next stage needs it
Each agent knows its own structure and formats appropriately
No wasted computation if no next stage exists
Works seamlessly with parallel agents (aggregate multiple executions)
```

### ✅ Flexible Queries

**Timeline for entire session:**
```go
events := client.TimelineEvent.Query().
    Where(timelineevent.SessionIDEQ(sessionID)).
    Order(ent.Asc(timelineevent.FieldSequenceNumber)).
    All(ctx)
```

**Timeline for a stage (all agents combined):**
```go
events := client.TimelineEvent.Query().
    Where(timelineevent.StageIDEQ(stageID)).
    Order(ent.Asc(timelineevent.FieldSequenceNumber)).
    All(ctx)
```

**Timeline for specific agent in parallel stage:**
```go
events := client.TimelineEvent.Query().
    Where(timelineevent.ExecutionIDEQ(executionID)).
    Order(ent.Asc(timelineevent.FieldSequenceNumber)).
    All(ctx)
```

**Messages for agent's conversation:**
```go
messages := client.Message.Query().
    Where(message.ExecutionIDEQ(executionID)).
    Order(ent.Asc(message.FieldSequenceNumber)).
    All(ctx)
```

**All stages in a session:**
```go
stages := client.Stage.Query().
    Where(stage.SessionIDEQ(sessionID)).
    Order(ent.Asc(stage.FieldStageIndex)).
    All(ctx)
```

**All agent executions for a stage:**
```go
executions := client.AgentExecution.Query().
    Where(agentexecution.StageIDEQ(stageID)).
    Order(ent.Asc(agentexecution.FieldAgentIndex)).
    All(ctx)
```

---

## Implementation Notes

### Event Lifecycle Examples

```
llm_thinking:
  Create with status='streaming' → Stream chunks → Update status='completed'

mcp_tool_call:
  Create with status='started' → Execute tool → Update status='completed' + result

user_question (chat):
  Create with status='completed' (instant, no streaming)
```

### Concurrency

- Single writer per event (the generating agent)
- Multiple readers (WebSocket subscribers)
- No optimistic locking needed
- Multiple agents can run concurrently, each writing their own events

### Key Architectural Changes from Old TARSy

**✅ Two-table hierarchy:** Stage (coordination) + AgentExecution (actual work)  
**✅ Lazy context building:** No pre-generated output fields, context built on-demand  
**✅ Parallel agent support:** Uniform model, no special "parent execution" entities  
**✅ No pause feature:** Removed pause/resume complexity (using forced_conclusion instead)  
**✅ Clean separation:** Timeline (UX) / Messages (LLM) / Interactions (Debug) all separate  

---

### Q2: Alert Data Storage & Search

**Status**: ✅ **RESOLVED**

**Context:**
- Alert data is passed as-is to LLM (no parsing or structure required)
- Need ability to search/filter by alert content in dashboard
- No need for structured field extraction (severity, cluster, etc.)

**Decision:**

```
AlertSession:
- alert_data          TEXT      Raw alert string (as received)
```

**Full-Text Search Implementation:**
Use PostgreSQL's built-in full-text search with GIN index:

```sql
-- Ent schema definition:
CREATE INDEX idx_alert_sessions_fts 
ON alert_sessions 
USING GIN(to_tsvector('english', alert_data));

-- Query examples:
-- Simple keyword search:
WHERE to_tsvector('english', alert_data) @@ to_tsquery('error');

-- Boolean operators (AND, OR, NOT):
WHERE to_tsvector('english', alert_data) @@ to_tsquery('error & critical');
WHERE to_tsvector('english', alert_data) @@ to_tsquery('error | warning');
WHERE to_tsvector('english', alert_data) @@ to_tsquery('error & !timeout');

-- Phrase search:
WHERE to_tsvector('english', alert_data) @@ phraseto_tsquery('out of memory');

-- With ranking:
SELECT *, ts_rank(to_tsvector('english', alert_data), to_tsquery('error')) as rank
FROM alert_sessions
WHERE to_tsvector('english', alert_data) @@ to_tsquery('error')
ORDER BY rank DESC;
```

**Benefits:**
- ✅ Very fast even on large datasets (GIN index)
- ✅ Supports stemming (search "running" finds "run", "runs", etc.)
- ✅ Boolean operators (AND, OR, NOT)
- ✅ Relevance ranking
- ✅ No complex parsing or field extraction needed
- ✅ Ent supports GIN indexes natively

**Optional Future Extension:**
If specific structured filtering becomes important later, can add:
```
+ alert_source        *string   Optional: filter by source (prometheus, k8s, custom)
```

---

### Q3: Stage Output Size Management

**Status**: ✅ **RESOLVED** (by Q1 decision)

**Original Problem:**
- `stage_output` JSON stored inline in database
- No size limits or constraints
- Large analysis outputs could bloat database rows
- PostgreSQL 1GB row size limit concern

**Resolution:**
- ✅ **No stage_output or agent_output fields** in the new schema!
- ✅ **Lazy context building** pattern eliminates this concern entirely
- ✅ Context generated on-demand from artifacts (Messages, TimelineEvents)
- ✅ No large JSON blobs stored in Stage or AgentExecution tables

---

## 📋 High Priority (Architecture Decisions)

### Q4: Chain Configuration Storage

**Status**: ✅ **RESOLVED**

**Context:**
In old TARSy, `AlertSession` stored both `chain_id` and full `chain_definition` JSON snapshot. The snapshot was used for:
1. **Pause/Resume** - **DROPPED in new TARSy** ✂️
2. **Chat configuration** - Check if chat enabled, get agent config, iteration strategy, LLM provider

**Analysis:**

For chat specifically, using a **live lookup** from registry makes more sense than snapshot:
- Chat happens **after** the investigation is complete (not part of immutable investigation record)
- Chat is a separate, optional interaction
- Using latest config means bug fixes and improvements apply to all chats
- No historical consistency requirement (unlike the investigation itself)

**Decision:**

```
AlertSession:
- chain_id            string    Chain identifier (indexed)
```

**No `chain_definition` snapshot stored in database.**

**Rationale:**
- ✅ **No duplication**: 1000 sessions = 1 chain_id string each, not 1000 JSON copies
- ✅ **Always current**: Chat and other features use latest chain configuration
- ✅ **Simpler schema**: One less JSON field to manage
- ✅ **Bug fixes propagate**: Chain config improvements benefit all sessions
- ✅ **Pause/resume dropped**: No need to restore exact historical chain state

**Chain lookup:**
When chat (or other features) needs chain config:
```go
// Look up current chain definition from registry
chainConfig := chainRegistry.GetChain(session.ChainID)
if chainConfig.Chat != nil && !chainConfig.Chat.Enabled {
    return ErrChatDisabled
}
```

**Note:** Chain definitions are stored in code/config files (e.g., `agents.yaml`), loaded at startup into in-memory registry. Not stored in database.

---

### Q5: Integration/Notification Data Modeling

**Status**: ✅ **RESOLVED**

**Context:**
Old TARSy has `slack_message_fingerprint` field directly in `AlertSession` for Slack threading support.

**Decision:**

Keep it simple - **Slack only** for now:

```
AlertSession:
- slack_message_fingerprint  *string  Optional: for Slack message threading
```

**Rationale:**
- ✅ **Simple**: No additional tables or complexity
- ✅ **Sufficient**: Slack is the only notification channel currently needed
- ✅ **Pragmatic**: Avoid premature abstraction
- ✅ **Refactorable**: Easy to extract to separate `Notification` entity later if needed

**Future Extension:**
If additional notification channels (Email, PagerDuty, webhooks) become necessary, refactor to:
```
Notification:
- notification_id     string    PK
- session_id          string    FK → AlertSession (indexed)
- notification_type   enum      slack, email, pagerduty, webhook
- integration_data    JSON      Type-specific data (channel, thread_ts, etc.)
- created_at          time.Time
```

For now: **Keep it simple, refactor when needed.**

---

### Q6: Timeline & Debug View Performance

**Status**: ✅ **RESOLVED**

**Context:**
Two different views with different requirements:

1. **Main Session Page** (UX-focused): Uses `TimelineEvent` entities (from Q1)
2. **Debug Page** (Observability): Uses `LLMInteraction` and `MCPInteraction` entities

**Architecture Decision: Separate Pages (Not Tabs)**

Split into two independent pages for better performance and separation of concerns.

---

### Main Session Page: `/sessions/{session_id}`

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

---

### Debug Page: `/sessions/{session_id}/debug`

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

---

**Database Indexes:**
```sql
-- Main Session Page
CREATE INDEX idx_timeline_events_session ON timeline_events(session_id, sequence_number);

-- Debug Page (list view)
CREATE INDEX idx_llm_interactions_session ON llm_interactions(session_id, created_at);
CREATE INDEX idx_mcp_interactions_session ON mcp_interactions(session_id, created_at);
```

---

**Benefits of Separate Pages:**
- ✅ **Much faster main page load**: Only loads what 95% of users need
- ✅ **Cleaner separation**: Reasoning and Debug are truly independent
- ✅ **Better performance**: Only pay for what you use
- ✅ **Simpler implementation**: No tab state management, clear API endpoints
- ✅ **Better for most users**: Debug data only loaded when explicitly navigated to
- ✅ **Independent WebSocket subscriptions**: Each page subscribes to only what it needs

---

## 📊 Medium Priority (Features & Observability)

### Q7: Audit Trail / Change Tracking

**Status**: ⏸️ **DEFERRED**

**Decision:**
Drop audit trail for now. Can implement later if needed.

**Options for future implementation:**
1. **Entity-level auditing**: Track DB changes (before/after snapshots)
2. **API request logging**: Log all API calls (simpler, captures intent + failures)

---

### Q8: LLM Cost Tracking

**Status**: ⏸️ **DEFERRED**

**Decision:**
No cost tracking for now. Store token counts only.

**Current Design:**
```
LLMInteraction:
- input_tokens        *int
- output_tokens       *int
- total_tokens        *int
```

---

### Q9: Event Table Retention & Cleanup

**Status**: ✅ **RESOLVED**

**Context:**
`Event` table used for WebSocket event distribution to live clients during active sessions.

**Decision:**

**Retention:**
- Events only needed for **active sessions**
- Used **only for live updates** (not historical replay)
- No need to retain after session completes

**Cleanup Strategy:**

**Option A: Automatic Cleanup on Session Completion (Ent)**
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

**Option B: TTL-based Cleanup (PostgreSQL + Ent)**
```go
// Add created_at timestamp to Event schema
Event:
+ created_at          time.Time Indexed

// Scheduled cleanup job (e.g., every hour via cron)
func cleanupOldEvents(ctx context.Context, client *ent.Client) error {
    cutoff := time.Now().Add(-24 * time.Hour)
    
    deleted, err := client.Event.
        Delete().
        Where(event.CreatedAtLT(cutoff)).
        Exec(ctx)
    
    log.Printf("Cleaned up %d old events", deleted)
    return err
}
```

**Recommendation: Option A (Session Completion Cleanup)**
- ✅ Simpler: Clean up exactly when no longer needed
- ✅ Efficient: Delete specific session's events
- ✅ Predictable: Events removed immediately on session completion
- ✅ No orphaned events: Handles edge cases (crashes, abandoned sessions)

**Fallback: Add TTL cleanup** as backup to handle edge cases where session completion hook doesn't run.

**Implementation (Ent):**
```go
// Primary: Clean on session completion
func cleanupSessionEvents(ctx context.Context, client *ent.Client, sessionID string) error {
    _, err := client.Event.
        Delete().
        Where(event.SessionIDEQ(sessionID)).
        Exec(ctx)
    return err
}

// Fallback: Periodic cleanup of old events (safety net)
// Cron job runs daily
func cleanupOrphanedEvents(ctx context.Context, client *ent.Client) error {
    cutoff := time.Now().Add(-7 * 24 * time.Hour)
    
    deleted, err := client.Event.
        Delete().
        Where(event.CreatedAtLT(cutoff)).
        Exec(ctx)
    
    log.Printf("Cleaned up %d orphaned events older than 7 days", deleted)
    return err
}
```

**Expected Size:**
- Active sessions at any time: ~10-100
- Events per session: ~50-200
- Total events in table: < 20K rows (very manageable)

---

## 💡 Low Priority (Nice-to-Have)

### Q10: Chat Conversation History Storage

**Status**: ✅ **RESOLVED**

**Context:**
Old TARSy stored `conversation_history` in `Chat` table - a formatted snapshot of the investigation for chat context.

**Q1 Impact:**
With **lazy context building** (Q1), we don't pre-generate or store `stage_output` or `agent_output`. Instead, context is built on-demand from artifacts.

**Decision:**

**No `conversation_history` field in `Chat` table.**

**Chat Context Building (On Chat Creation):**
```go
// When user starts a chat, build context on-demand
func (s *ChatService) CreateChat(ctx context.Context, sessionID string) (*Chat, error) {
    // Query session artifacts
    timelineEvents := s.getTimelineEvents(ctx, sessionID)
    messages := s.getMessages(ctx, sessionID)
    
    // Build chat context using ChatAgent's context builder
    // (each agent type knows how to build context from its artifacts)
    chatContext := s.chatAgent.BuildContextForChat(timelineEvents, messages)
    
    // Create chat record (no conversation_history stored)
    chat := &Chat{
        SessionID:  sessionID,
        CreatedBy:  userID,
        // No conversation_history field
    }
    
    return s.client.Chat.Create().SetChat(chat).Save(ctx)
}
```

**Chat Schema (Simplified):**
```
Chat:
- chat_id             string    PK
- session_id          string    FK → AlertSession (indexed)
- created_by          string    User who initiated chat
- created_at          time.Time
- mcp_selection       JSON      Optional MCP override
```

**Benefits:**
- ✅ **No duplication**: Don't store data that exists in TimelineEvents/Messages
- ✅ **Always current**: Context built from latest artifacts (if artifacts update, chat sees it)
- ✅ **Consistent with Q1**: Lazy evaluation pattern throughout
- ✅ **Less storage**: One less large TEXT/JSON field per chat

**When chat message sent:**
Context is already in memory from chat creation, or rebuilt from artifacts if needed (e.g., server restart, long-running chat).

---

### Q11: Search & Analytics Support

**Status**: ✅ **RESOLVED**

**Decisions:**

**Q11.1: Full-text search on final analysis**
- **Decision**: Support it (nice to have)
- **Implementation**: Same as Q2 (alert_data)

```
AlertSession:
- final_analysis      TEXT      Investigation summary

-- Add GIN index for full-text search
CREATE INDEX idx_alert_sessions_final_analysis_fts 
ON alert_sessions 
USING GIN(to_tsvector('english', final_analysis));

-- Query examples:
WHERE to_tsvector('english', final_analysis) @@ to_tsquery('memory & leak');
WHERE to_tsvector('english', final_analysis) @@ to_tsquery('error | failure');
```

**Benefits:**
- ✅ Search within investigation summaries
- ✅ Find sessions by analysis keywords
- ✅ Same pattern as alert_data full-text search

---

**Q11.2: Common aggregations**
- **Decision**: Not needed for now
- **Rationale**: Built-in dashboard queries sufficient
- **Future**: Can add materialized views or aggregation tables if performance becomes an issue

---

**Q11.3: BI/Analytics export**
- **Decision**: Out of scope for now
- **Future**: Direct PostgreSQL access or export APIs if needed

---

### Q12: Soft Deletes vs Hard Deletes

**Status**: ✅ **RESOLVED**

**Context:**
- No manual deletion support for now
- Need retention policy for old sessions
- May need to restore soft-deleted sessions if needed

**Decision: Soft Delete for Retention Policy**

**Schema:**
```
AlertSession:
+ deleted_at          *time.Time  Soft delete timestamp (null = active)
```

**Implementation (Ent):**

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

// Hard delete (final cleanup, e.g., after 1 year)
func hardDeleteOldSessions(ctx context.Context, client *ent.Client) error {
    cutoff := time.Now().Add(-365 * 24 * time.Hour)
    
    deleted, err := client.AlertSession.
        Delete().
        Where(
            alertsession.DeletedAtNotNil(),      // Only soft-deleted
            alertsession.DeletedAtLT(cutoff),    // Older than 1 year
        ).
        Exec(ctx)
    
    log.Printf("Hard deleted %d soft-deleted sessions older than 1 year", deleted)
    return err
}
```

**Retention Policy Example:**
1. **Day 0-90**: Active sessions (visible in dashboard)
2. **Day 90-365**: Soft-deleted (hidden, but restorable if needed)
3. **Day 365+**: Hard-deleted (permanently removed via CASCADE)

**Benefits:**
- ✅ **Safety net**: Can restore accidentally removed sessions
- ✅ **Gradual cleanup**: Two-phase deletion (soft → hard)
- ✅ **Simple queries**: Just add `WHERE deleted_at IS NULL` for active data
- ✅ **Ent support**: Native Ent mixin for soft deletes

**Index:**
```sql
CREATE INDEX idx_alert_sessions_deleted_at ON alert_sessions(deleted_at) 
WHERE deleted_at IS NOT NULL;
```

---

## 📝 Summary Checklist

Track which questions we've addressed:

### Critical Priority
- [x] Q1: Database Schema Architecture - Multi-Layer Model (Stage + AgentExecution + TimelineEvent + Message + LLM/MCPInteraction + Lazy Context Building) ✅ **DECIDED**
- [x] Q2: Alert Data Storage & Search (TEXT field + PostgreSQL full-text search with GIN index) ✅ **RESOLVED**
- [x] Q3: Stage Output Size Management ✅ **RESOLVED** (by Q1 - no output fields)

### High Priority
- [x] Q4: Chain Configuration Storage (Just `chain_id`, no snapshot - live lookup from registry) ✅ **RESOLVED**
- [x] Q5: Integration/Notification Modeling (Keep simple: `slack_message_fingerprint` in AlertSession, refactor later if needed) ✅ **RESOLVED**
- [x] Q6: Timeline & Debug View Performance (Reasoning: TimelineEvent query; Debug: 2-level loading with lazy expansion) ✅ **RESOLVED**

### Medium Priority
- [x] Q7: Audit Trail ⏸️ **DEFERRED** (Can implement later if needed)
- [x] Q8: LLM Cost Tracking ⏸️ **DEFERRED** (Token counts stored, no cost calculation for now)
- [x] Q9: Event Retention (Active sessions only, automatic cleanup on completion + TTL fallback) ✅ **RESOLVED**

### Low Priority
- [x] Q10: Chat Conversation History Storage (No storage - build on-demand from artifacts, consistent with Q1 lazy evaluation) ✅ **RESOLVED**
- [x] Q11: Search & Analytics Support (Full-text search on final_analysis, no special aggregations/BI for now) ✅ **RESOLVED**
- [x] Q12: Soft Deletes (Soft delete with `deleted_at` for retention policy, two-phase cleanup) ✅ **RESOLVED**

---

## Next Steps

1. Go through each question in order
2. Add answers inline under each question
3. Mark status (✅ Decided / ❌ Rejected / ⏸️ Deferred)
4. Update main design document based on decisions
5. Generate updated Ent schema definitions
