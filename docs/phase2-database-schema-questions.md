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

### Q2: Alert Data Extraction for Query Performance

**Status**: ⏸️

**Current Design:**
- `alert_data` JSON blob contains everything
- Queries like `WHERE alert_data->>'severity' = 'critical'` are slow
- Dashboard filters have to do expensive JSON extraction

**Common Query Examples:**
```sql
-- Slow (full table scan with JSON extraction):
SELECT * FROM alert_sessions 
WHERE alert_data->>'severity' = 'critical'
  AND alert_data->>'cluster' = 'prod-us-east'
  AND alert_data->>'namespace' LIKE 'app-%';
```

**Proposed Solution:**
Extract commonly-queried fields to top-level columns:
```
AlertSession:
- alert_data          JSON      Keep full payload for completeness
+ severity            *string   Indexed (critical, warning, info, etc.)
+ cluster             *string   Indexed (prod-us-east, staging-eu, etc.)
+ namespace           *string   Indexed
+ alert_source        *string   Indexed (prometheus, kubernetes, custom)
+ environment         *string   Indexed (prod, staging, dev)
+ resource_type       *string   (pod, deployment, node, etc.)
+ resource_name       *string
```

**Questions for You:**

**Q2.1**: What are the most common filters you use in the dashboard?
List in order of frequency:
1. 
2. 
3. 
4. 
5. 

**Answer:**

---

**Q2.2**: Which fields from `alert_data` do you search/filter by regularly?
(e.g., severity, cluster, namespace, alert_type, etc.)

**Answer:**

---

**Q2.3**: Is the alert structure consistent across different alert types?
- All alerts have same structure? 
- Each alert type has different fields?
- Some common fields + type-specific fields?

**Answer:**

---

**Q2.4**: Do you need full-text search on alert content?
- Search in alert messages/descriptions?
- Search in final analysis text?

**Answer:**

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

**Original Proposed Solutions (no longer needed):**

**Option A: Size Limit + Overflow Storage**
```
StageExecution:
- stage_output        JSON      Limited to 10KB
- stage_output_overflow_id *string  FK → BlobStorage (for >10KB outputs)

BlobStorage:
- blob_id             string    PK
- content_type        string    'application/json'
- size_bytes          int
- storage_url         string    S3/MinIO URL or file path
- created_at          time.Time
```

**Option B: Summary + Full Split**
```
StageExecution:
- stage_output_summary  JSON    1KB limit, key results only
- stage_output_full_url *string External storage URL
```

**Option C: Keep Current (No Limits)**
- Accept that some rows will be large
- Rely on PostgreSQL TOAST for compression
- Monitor table bloat

**Questions for You:**

**Q3.1**: How large do stage outputs typically get in old TARSy?
- Usually under 1KB?
- Sometimes 10-100KB?
- Often over 100KB?
- Ever hit MB range?

**Answer:**

---

**Q3.2**: When viewing session history, do you need full stage output or just summary?
- **List view**: Just summary/status?
- **Detail view**: Full output?
- **Always full output**?

**Answer:**

---

**Q3.3**: Are you willing to use object storage (S3/MinIO/filesystem) for large outputs?
- Yes, S3-compatible storage is fine
- Yes, but prefer filesystem for simplicity
- No, keep everything in PostgreSQL

**Answer:**

---

**Q3.4**: What's an acceptable size limit for inline storage before moving to external?
- 10KB?
- 50KB?
- 100KB?
- No limit?

**Answer:**

---

---

## 📋 High Priority (Architecture Decisions)

### Q4: Chain Configuration - Versioning & Duplication

**Status**: ⏸️

**Current Design:**
- Every `AlertSession` stores full `chain_definition` JSON
- 1000 sessions with same chain = 1000 copies
- No way to track "which sessions used chain version X"

**Proposed Solutions:**

**Option A: Chain Entity (Normalized)**
```
Chain:
- chain_id            string    PK
- name                string    Indexed
- version             string    Semantic version (e.g., "1.2.3")
- definition          JSON      Chain config
- created_at          time.Time
- deprecated_at       *time.Time

AlertSession:
- chain_id            string    FK → Chain
- chain_overrides     JSON      Session-specific tweaks (optional)
```

**Option B: Version Reference + Snapshot**
```
AlertSession:
- chain_name          string    Indexed (e.g., "kubernetes-investigation")
- chain_version       string    Indexed (e.g., "v2.1.0")
- chain_definition    JSON      Full snapshot (for immutability)
```

**Option C: Keep Current (Just Snapshot)**
- No normalization
- Accept duplication
- Simple, immutable

**Questions for You:**

**Q4.1**: How often do chain definitions change?
- Rarely (every few months)?
- Regularly (weekly)?
- Frequently (multiple times per week)?

**Answer:**

---

**Q4.2**: Do you need to query "all sessions that used chain X version Y"?
- Yes, for impact analysis when chains change
- No, not really needed
- Would be nice to have

**Answer:**

---

**Q4.3**: Are chain definitions large?
- Small (~1-5KB)?
- Medium (~10-50KB)?
- Large (>50KB)?

**Answer:**

---

**Q4.4**: Do you ever need to update chain config retroactively for old sessions?
- No, sessions are immutable
- Yes, for bug fixes (but keep history)
- Sometimes, for reprocessing

**Answer:**

---

### Q5: Integration/Notification Data Modeling

**Status**: ⏸️

**Current Design:**
- `slack_message_fingerprint` field directly in `AlertSession`
- Couples core domain to specific integration

**The Problem:**
- What about Email, PagerDuty, webhooks, etc.?
- Can't have multiple notifications per session
- Hard to add new notification types

**Proposed Solution:**
Separate `Notification` entity:
```
Notification:
- notification_id     string    PK
- session_id          string    FK → AlertSession (indexed)
- notification_type   enum      slack, email, pagerduty, webhook, msteams
- status              enum      pending, sent, delivered, failed
- integration_data    JSON      Type-specific data
- sent_at             *time.Time
- delivered_at        *time.Time
- error_message       *string
- retry_count         int

For Slack specifically:
integration_data = {
  "channel": "#alerts",
  "thread_ts": "1234567890.123456",
  "message_fingerprint": "abc123",
  "permalink": "https://..."
}
```

**Questions for You:**

**Q5.1**: Do you plan to support multiple notification channels beyond Slack?
- Yes, definitely (which ones?)
- Maybe in the future
- No, Slack only for now

**Answer:**

---

**Q5.2**: Can one session have notifications to multiple destinations?
- Yes (e.g., Slack + Email + PagerDuty)
- No, one notification per session
- One per type (one Slack, one Email, etc.)

**Answer:**

---

**Q5.3**: Should chat responses also trigger notifications?
- Yes, notify on chat responses
- No, only investigation completion
- Configurable per user/chat

**Answer:**

---

**Q5.4**: Do you need notification delivery status tracking?
- Yes, critical (sent/delivered/failed/retry)
- Basic (sent/failed)
- No, fire-and-forget

**Answer:**

---

### Q6: Timeline Reconstruction Performance

**Status**: ⏸️

**Current Design:**
To build session timeline:
1. Query session
2. Query all stage executions
3. Query all LLM interactions
4. Query all MCP interactions  
5. Merge and sort in application code

**The Problem:**
- 4+ database queries per timeline view
- No pagination support
- Expensive for sessions with 100+ interactions
- Timeline is read-heavy, write-once

**Proposed Solutions:**

**Option A: Materialized View (PostgreSQL)**
```sql
CREATE MATERIALIZED VIEW session_timeline AS
SELECT 
  session_id,
  event_id,
  'llm' as event_type,
  timestamp,
  stage_execution_id,
  jsonb_build_object(
    'model', model_name,
    'duration_ms', duration_ms
  ) as summary
FROM llm_interactions
UNION ALL
SELECT 
  session_id,
  interaction_id as event_id,
  'mcp' as event_type,
  timestamp,
  stage_execution_id,
  jsonb_build_object(
    'tool', tool_name,
    'server', server_name
  ) as summary
FROM mcp_interactions
UNION ALL ...;

CREATE INDEX idx_timeline_session ON session_timeline(session_id, timestamp);
REFRESH MATERIALIZED VIEW CONCURRENTLY session_timeline;
```

**Option B: Timeline Cache Table (Updated in Real-Time)**
```
TimelineEvent:
- event_id            string    PK
- session_id          string    FK → AlertSession (indexed)
- event_type          enum      llm, mcp, stage_start, stage_end, status_change
- timestamp           time.Time Indexed
- stage_execution_id  *string   FK → StageExecution
- summary_data        JSON      Small, denormalized summary
- source_table        string    'llm_interactions', 'mcp_interactions', etc.
- source_id           string    FK to source table

Index: (session_id, timestamp) for fast timeline retrieval
```

**Option C: Keep Current + Add Pagination**
- Keep separate queries
- Add cursor-based pagination (by timestamp)
- Use Ent's eager loading to minimize queries

**Questions for You:**

**Q6.1**: How often is timeline viewed compared to session creation?
- Very frequent (every session viewed multiple times)
- Moderate (sessions viewed 1-2 times)
- Rare (only for debugging/deep analysis)

**Answer:**

---

**Q6.2**: Is real-time timeline updates critical during active session?
- Yes, need instant updates (WebSocket already provides this)
- No, eventual consistency is fine (can refresh)

**Answer:**

---

**Q6.3**: What's typical session interaction count?
- Usually <20 interactions
- Often 20-50 interactions
- Sometimes 50-100 interactions
- Can exceed 100 interactions

**Answer:**

---

**Q6.4**: Do you paginate timeline display in the UI?
- Yes, show 20-50 at a time
- No, load all interactions at once
- Load incrementally as user scrolls

**Answer:**

---

---

## 📊 Medium Priority (Features & Observability)

### Q7: Audit Trail / Change Tracking

**Status**: ⏸️

**Current Design:**
- No audit trail
- Can't see who cancelled a session, when status changed, etc.

**Use Cases:**
- Security: "Who manually cancelled session X?"
- Debugging: "When did this session fail? What changed?"
- Compliance: "Show all manual interventions"

**Proposed Solution:**
```
AuditLog:
- log_id              string    PK
- entity_type         string    'AlertSession', 'StageExecution', etc.
- entity_id           string    ID of the entity
- action              enum      created, updated, deleted, status_changed, cancelled
- user_id             *string   From oauth2-proxy (null = system)
- changes             JSON      Before/after snapshot
- reason              *string   User-provided reason for change
- timestamp           time.Time Indexed
- ip_address          *string
- user_agent          *string
```

**Questions for You:**

**Q7.1**: Do you need audit trail for security/compliance reasons?
- Yes, required
- Nice to have
- Not needed

**Answer:**

---

**Q7.2**: Which entities need auditing?
- Just AlertSession (status changes, cancellation)
- AlertSession + StageExecution
- All entities
- Configuration changes (chains, MCP servers)

**Answer:**

---

**Q7.3**: How long to retain audit logs?
- Same as session data
- Longer (for compliance)
- Forever
- Not applicable

**Answer:**

---

### Q8: LLM Cost Tracking

**Status**: ⏸️

**Current Design:**
- Token counts stored (`input_tokens`, `output_tokens`)
- No cost calculation
- No model pricing data

**The Problem:**
- Can't answer: "How much did this investigation cost?"
- Can't track costs over time
- Can't compare cost across models/chains

**Proposed Solution:**

**Option A: Add Cost Fields to LLMInteraction**
```
LLMInteraction:
+ input_cost_usd      *decimal  Calculated cost for input tokens
+ output_cost_usd     *decimal  Calculated cost for output tokens
+ total_cost_usd      *decimal  Total cost
+ pricing_snapshot    JSON      Model rates at time of call
  {
    "model": "gemini-2.0-flash-thinking-exp",
    "input_price_per_1m": 0.10,
    "output_price_per_1m": 0.40,
    "pricing_date": "2026-02-01"
  }
```

**Option B: Separate Cost Tracking Table**
```
LLMCost:
- cost_id             string    PK
- interaction_id      string    FK → LLMInteraction
- model_name          string
- input_cost_usd      decimal
- output_cost_usd     decimal
- total_cost_usd      decimal
- pricing_tier        string    Account tier/pricing plan
- calculated_at       time.Time
```

**Option C: No Cost Tracking**
- Calculate on-demand from token counts
- Keep pricing in application config

**Questions for You:**

**Q8.1**: Do you need cost tracking and reporting?
- Yes, critical for budget management
- Nice to have for visibility
- Not needed

**Answer:**

---

**Q8.2**: Should costs be calculated real-time or batch?
- Real-time (during LLM call)
- Batch (daily/weekly job)
- On-demand (when viewing reports)

**Answer:**

---

**Q8.3**: Do you need historical cost data if pricing changes?
- Yes, need to see actual costs paid
- No, current pricing is fine
- Need both (actual + estimated at current rates)

**Answer:**

---

**Q8.4**: Different pricing for different users/accounts?
- No, same pricing for all
- Yes, different tiers/accounts
- Not applicable

**Answer:**

---

### Q9: Event Table Retention & Cleanup

**Status**: ⏸️

**Current Design:**
- `Event` table for WebSocket event distribution
- Auto-incrementing ID
- No explicit cleanup strategy

**The Problem:**
- Events accumulate forever
- Table grows unbounded
- Old events never useful after session completes

**Questions for You:**

**Q9.1**: How long do events need to be retained?
- Only while session is active
- 1 day after session completes
- 1 week after session completes
- Indefinitely (for replay)

**Answer:**

---

**Q9.2**: Are events used for historical replay, or only live sessions?
- Only for live WebSocket clients (catchup on reconnect)
- Also for historical "replay investigation" feature
- Both

**Answer:**

---

**Q9.3**: Should cleanup be automatic or manual?
- Automatic (PostgreSQL trigger/cron)
- Manual (admin cleanup command)
- No cleanup needed

**Answer:**

---

**Q9.4**: What's an acceptable event table size?
- Keep small (< 10K rows)
- Medium (< 100K rows)
- Large (< 1M rows)
- Unlimited

**Answer:**

---

---

## 💡 Low Priority (Nice-to-Have)

### Q10: Chat Conversation History Duplication

**Status**: ⏸️

**Current Design:**
- `Chat.conversation_history` stores formatted investigation text
- This duplicates data from session's LLM interactions and stage outputs

**Questions for You:**

**Q10.1**: Is `conversation_history` just a formatted version of the investigation?
- Yes, generated from stage outputs
- No, it includes additional formatting/summaries
- It's a mix

**Answer:**

---

**Q10.2**: Can it be reliably reconstructed from stage outputs?
- Yes, easy to regenerate
- Yes, but expensive computation
- No, has manual edits/additions

**Answer:**

---

**Q10.3**: Is it expensive to generate, justifying caching?
- Yes, expensive (keep cached)
- No, quick (regenerate on demand)
- Moderate

**Answer:**

---

### Q11: Common Field Extraction (Additional)

**Status**: ⏸️

**Questions for Dashboard Usage:**

**Q11.1**: Do you need full-text search on final analysis?
- Yes, search within analysis text
- No, just filter by metadata
- Would be nice to have

**Answer:**

---

**Q11.2**: Are there common aggregations you need?
Examples:
- Count by severity per day
- Average duration by alert type
- Token usage by chain
- Success/failure rate by agent

**Answer:**

---

**Q11.3**: Do you export data for external analytics (BI tools)?
- Yes, need periodic exports
- Yes, real-time BI integration
- No, built-in dashboard is enough

**Answer:**

---

### Q12: Soft Deletes vs Hard Deletes

**Status**: ⏸️

**Current Design:**
- Hard deletes with CASCADE

**Options:**
- **Hard Delete**: Data is gone (use retention policy)
- **Soft Delete**: Add `deleted_at` field, hide from queries
- **Archive**: Move to separate archive tables

**Questions for You:**

**Q12.1**: Do you ever need to "undelete" sessions?
- Yes, accidental deletions happen
- No, deletion is final
- Only for retention policy (not manual deletes)

**Answer:**

---

**Q12.2**: Should deleted sessions be hidden or fully removed?
- Hidden (soft delete) - can restore
- Removed (hard delete) - better for GDPR
- Archived (separate table)

**Answer:**

---

---

## 📝 Summary Checklist

Track which questions we've addressed:

### Critical Priority
- [x] Q1: Database Schema Architecture - Multi-Layer Model (Stage + AgentExecution + TimelineEvent + Message + LLM/MCPInteraction + Lazy Context Building) ✅ **DECIDED**
- [ ] Q2: Alert Data Extraction  
- [x] Q3: Stage Output Size Management ✅ **RESOLVED** (by Q1 - no output fields)

### High Priority
- [ ] Q4: Chain Configuration Versioning
- [ ] Q5: Integration/Notification Modeling
- [ ] Q6: Timeline Reconstruction Performance

### Medium Priority
- [ ] Q7: Audit Trail
- [ ] Q8: LLM Cost Tracking
- [ ] Q9: Event Retention

### Low Priority
- [ ] Q10: Chat History Duplication
- [ ] Q11: Common Field Extraction
- [ ] Q12: Soft Deletes

---

## Next Steps

1. Go through each question in order
2. Add answers inline under each question
3. Mark status (✅ Decided / ❌ Rejected / ⏸️ Deferred)
4. Update main design document based on decisions
5. Generate updated Ent schema definitions
