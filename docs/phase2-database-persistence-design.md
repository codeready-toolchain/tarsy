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

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────┐
│                     Go Service Layer                    │
│  (Business Logic, Transaction Management, Validation)   │
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
│          (Sessions, Stages, Interactions, Events)       │
└─────────────────────────────────────────────────────────┘
```

---

## Database Schema Design

### Core Entities (Ent Schema)

#### 1. **AlertSession**
Primary entity tracking alert processing sessions.

**Fields:**
```
session_id          string    PK, UUID
alert_data          JSON      Original alert payload
agent_type          string    Agent type (e.g., 'kubernetes')
alert_type          string    Alert classification (indexed)
status              enum      Current status (indexed)
started_at          time.Time Indexed
completed_at        *time.Time
error_message       *string
final_analysis      *string
final_analysis_summary *string
executive_summary_error *string
session_metadata    JSON
pause_metadata      JSON      Why/when paused
author              *string   From oauth2-proxy
runbook_url         *string
mcp_selection       JSON      MCP override config

chain_id            string    Chain identifier (indexed)
chain_definition    JSON      Chain config snapshot
current_stage_index *int
current_stage_id    *string

pod_id              *string   For multi-replica coordination
last_interaction_at *time.Time For orphan detection (indexed)
slack_message_fingerprint *string For Slack threading
```

**Indexes:**
- `status` - For queue queries
- `agent_type` - Filtering
- `alert_type` - Filtering  
- `(status, started_at)` - Most common query pattern
- `(status, last_interaction_at)` - Orphan detection
- `chain_id` - Chain-based queries

**Enums:**
- `Status`: `pending`, `in_progress`, `completed`, `failed`, `paused`, `cancelled`

**Edges:**
- `stages` → `StageExecution[]` (cascade delete)
- `llm_interactions` → `LLMInteraction[]` (cascade delete)
- `mcp_interactions` → `MCPInteraction[]` (cascade delete)
- `chat` → `Chat` (optional, cascade delete)

**🔄 CHANGES FROM OLD TARSY:**
- ✅ **KEEP**: Core structure, status enum, indexes
- ⚠️ **CHANGE**: Remove `started_at_us`/`completed_at_us` (microseconds) → Use `time.Time` (Go idiomatic, Ent handles precision)
- ⚠️ **CHANGE**: Add proper enum types instead of string constants
- ➕ **ADD**: Composite indexes for common query patterns

---

#### 2. **StageExecution**
Tracks individual chain stage executions.

**Fields:**
```
execution_id        string    PK, UUID
session_id          string    FK → AlertSession (indexed)
stage_id            string    Stage identifier
stage_index         int       0-based position (indexed)
stage_name          string    Human-readable name
agent               string    Agent type for stage
status              enum      Stage status
started_at          *time.Time
paused_at           *time.Time
completed_at        *time.Time
duration_ms         *int
stage_output        JSON      Success output
error_message       *string   Failure message
current_iteration   *int      For pause/resume
iteration_strategy  *string   e.g., 'react', 'native_thinking'

chat_id             *string   FK → Chat (if chat stage)
chat_user_message_id *string  FK → ChatUserMessage

parent_stage_execution_id *string FK → StageExecution (for parallel)
parallel_index      int       0 for parent, 1-N for children
parallel_type       enum      'single', 'multi_agent', 'replica'
expected_parallel_count *int  For parent stages
```

**Indexes:**
- `session_id` - Session lookup (with FK)
- `stage_index` - Ordering
- `parent_stage_execution_id` - Parallel stage queries
- `chat_id` - Chat stage queries

**Enums:**
- `StageStatus`: `pending`, `active`, `completed`, `failed`, `cancelled`, `paused`
- `ParallelType`: `single`, `multi_agent`, `replica`

**Edges:**
- `session` → `AlertSession`
- `llm_interactions` → `LLMInteraction[]`
- `mcp_interactions` → `MCPInteraction[]`
- `chat` → `Chat` (optional)
- `chat_user_message` → `ChatUserMessage` (optional)
- `parent` → `StageExecution` (self-reference)
- `children` → `StageExecution[]` (inverse of parent)

**🔄 CHANGES FROM OLD TARSY:**
- ✅ **KEEP**: Core structure, parallel execution support
- ⚠️ **CHANGE**: `time.Time` instead of microsecond integers
- ⚠️ **CHANGE**: Use Ent edges instead of manual foreign keys
- ➕ **ADD**: Better enum types
- ➕ **ADD**: Self-referential edge for parent/child stages

---

#### 3. **LLMInteraction**
Tracks LLM API calls and responses.

**Fields:**
```
interaction_id      string    PK, UUID
session_id          string    FK → AlertSession (indexed)
stage_execution_id  *string   FK → StageExecution (indexed, optional)
timestamp           time.Time Indexed
interaction_type    enum      'iteration', 'final_analysis', etc.
model_name          string    e.g., 'gemini-2.0-flash-thinking-exp'
conversation        JSON      Full conversation history
llm_request         JSON      Request payload
llm_response        JSON      Response payload
prompt_text         *string   Rendered prompt
thinking_text       *string   Native thinking output
response_text       *string   Assistant response
duration_ms         *int
input_tokens        *int
output_tokens       *int
total_tokens        *int
temperature         *float64
error_message       *string
```

**Indexes:**
- `session_id` - Session queries
- `stage_execution_id` - Stage queries
- `timestamp` - Timeline ordering
- `interaction_type` - Type filtering

**Enums:**
- `InteractionType`: `iteration`, `final_analysis`, `executive_summary`, `chat_response`

**Edges:**
- `session` → `AlertSession`
- `stage` → `StageExecution` (optional)

**🔄 CHANGES FROM OLD TARSY:**
- ✅ **KEEP**: Core fields, token tracking
- ⚠️ **CHANGE**: `timestamp` instead of `timestamp_us`
- ⚠️ **CHANGE**: Proper enum for interaction types
- ➕ **ADD**: Better indexing strategy

---

#### 4. **MCPInteraction**
Tracks MCP tool calls and results.

**Fields:**
```
communication_id    string    PK, UUID
session_id          string    FK → AlertSession (indexed)
stage_execution_id  *string   FK → StageExecution (indexed, optional)
timestamp           time.Time Indexed
communication_type  enum      'tool_call', 'tool_list', 'server_init'
server_name         string    MCP server name
tool_name           *string   Tool name (for tool_call)
step_description    string    Human-readable description
tool_arguments      JSON      Tool input
tool_result         JSON      Tool output
available_tools     JSON      For tool_list type
success             bool
error_message       *string
duration_ms         *int
```

**Indexes:**
- `session_id` - Session queries
- `stage_execution_id` - Stage queries
- `timestamp` - Timeline ordering
- `server_name` - Server-based queries

**Enums:**
- `CommunicationType`: `tool_call`, `tool_list`, `server_init`, `server_error`

**Edges:**
- `session` → `AlertSession`
- `stage` → `StageExecution` (optional)

**🔄 CHANGES FROM OLD TARSY:**
- ✅ **KEEP**: Core structure
- ⚠️ **CHANGE**: `timestamp` instead of `timestamp_us`
- ⚠️ **CHANGE**: Enum for communication types

---

#### 5. **Event**
Event persistence for cross-pod distribution and catchup.

**Fields:**
```
id                  int       PK, Auto-increment
channel             string    Event channel (indexed)
payload             JSON      Event data
created_at          time.Time Auto-set (indexed)
```

**Indexes:**
- `channel` - Channel filtering
- `created_at` - Cleanup queries
- `(channel, id)` - Polling queries

**🔄 CHANGES FROM OLD TARSY:**
- ✅ **KEEP**: Exact same structure (works well)
- ⚠️ **CHANGE**: Use Ent's auto-timestamp feature

---

#### 6. **Chat**
Chat metadata for follow-up conversations.

**Fields:**
```
chat_id             string    PK, UUID
session_id          string    FK → AlertSession (unique, indexed)
created_at          time.Time
created_by          *string   User email
conversation_history string   Formatted investigation text
chain_id            string    From original session
mcp_selection       JSON      MCP config from session
context_captured_at time.Time When context was snapshotted
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
- `stage_executions` → `StageExecution[]` (chat stages)

**🔄 CHANGES FROM OLD TARSY:**
- ✅ **KEEP**: Core structure
- ⚠️ **CHANGE**: `time.Time` instead of microseconds

---

#### 7. **ChatUserMessage**
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
- `stage_execution` → `StageExecution` (response stage)

**🔄 CHANGES FROM OLD TARSY:**
- ✅ **KEEP**: Same structure

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
│   ├── stageexecution.go
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
    "fmt"
    "time"
    
    "github.com/codeready-toolchain/tarsy/ent"
    "github.com/codeready-toolchain/tarsy/ent/migrate"
    _ "github.com/lib/pq" // PostgreSQL driver
)

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

func NewClient(ctx context.Context, cfg Config) (*ent.Client, error) {
    dsn := fmt.Sprintf(
        "host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
        cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database, cfg.SSLMode,
    )
    
    client, err := ent.Open("postgres", dsn)
    if err != nil {
        return nil, fmt.Errorf("failed to open database: %w", err)
    }
    
    // Configure connection pool
    db := client.Driver().(*sql.DB)
    db.SetMaxOpenConns(cfg.MaxOpenConns)
    db.SetMaxIdleConns(cfg.MaxIdleConns)
    db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
    db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
    
    // Run migrations
    if err := runMigrations(ctx, client); err != nil {
        client.Close()
        return nil, fmt.Errorf("failed to run migrations: %w", err)
    }
    
    return client, nil
}

func runMigrations(ctx context.Context, client *ent.Client) error {
    // Run migrations from versioned migration files
    return client.Schema.Create(
        ctx,
        migrate.WithDir("file://ent/migrate/migrations"),
        migrate.WithDropIndex(true),
        migrate.WithDropColumn(true),
    )
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
        SetChainID(req.ChainID).
        SetChainDefinition(req.ChainDefinition).
        Save(ctx)
    if err != nil {
        return nil, err
    }
    
    // Create initial stage executions (if needed)
    // ...
    
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

### Strategy

**All database operations must accept `context.Context`:**

```go
// Good: Context-aware query
func (s *SessionService) GetSession(
    ctx context.Context,
    sessionID string,
) (*ent.AlertSession, error) {
    return s.client.AlertSession.Query().
        Where(alertsession.SessionIDEQ(sessionID)).
        First(ctx) // Context passed through
}

// Good: Timeout support
ctx, cancel := context.WithTimeout(parentCtx, 5*time.Second)
defer cancel()

session, err := sessionService.GetSession(ctx, sessionID)
// Query will be cancelled after 5s
```

**Benefits:**
- HTTP request cancellation propagates to DB queries
- Manual cancellation (user action, shutdown)
- Deadlock prevention with timeouts
- Distributed tracing integration (future)

**🔄 CHANGES FROM OLD TARSY:**
- ➕ **ADD**: Consistent context usage throughout
- ➕ **IMPROVE**: Better cancellation semantics (Go standard)

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

**Test Structure:**
```go
func TestSessionService_CreateSession(t *testing.T) {
    client := NewTestClient(t)
    service := NewSessionService(client)
    
    ctx := context.Background()
    session, err := service.CreateSession(ctx, CreateSessionRequest{
        SessionID: "test-123",
        // ...
    })
    
    require.NoError(t, err)
    assert.Equal(t, "test-123", session.SessionID)
}
```

**🔄 CHANGES FROM OLD TARSY:**
- ➕ **IMPROVE**: testcontainers for isolation
- ➕ **IMPROVE**: No SQLite test mode (test against real DB)

---

## Performance Considerations

### Indexing Strategy

**Critical Indexes:**
1. `AlertSession(status, started_at)` - Queue queries
2. `AlertSession(status, last_interaction_at)` - Orphan detection
3. `StageExecution(session_id)` - Session stages lookup
4. `LLMInteraction(session_id, timestamp)` - Timeline reconstruction
5. `MCPInteraction(session_id, timestamp)` - Timeline reconstruction

### Query Optimization

**Use Ent's Query Optimizations:**
```go
// Eager loading with edges
sessions, err := client.AlertSession.Query().
    WithStages(func(q *ent.StageExecutionQuery) {
        q.WithLLMInteractions()
        q.WithMCPInteractions()
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
```

### JSON Field Queries

**PostgreSQL JSON operators:**
```go
// Query by JSON field (Ent supports raw SQL predicates)
sessions, err := client.AlertSession.Query().
    Where(func(s *sql.Selector) {
        s.Where(sql.P(func(b *sql.Builder) {
            b.WriteString("alert_data->>'severity' = ?")
            b.Arg("critical")
        }))
    }).
    All(ctx)
```

**🔄 CHANGES FROM OLD TARSY:**
- ➕ **IMPROVE**: Ent's eager loading (N+1 query prevention)
- ➕ **IMPROVE**: Better type safety in queries

---

## Implementation Checklist

### Phase 2.1: Schema & Migrations
- [ ] Define Ent schemas for all entities
- [ ] Configure enum types
- [ ] Set up edges/relationships
- [ ] Generate initial migration
- [ ] Test migration on fresh database
- [ ] Document schema in generated docs

### Phase 2.2: Database Client
- [ ] Implement database client initialization
- [ ] Add connection pool configuration
- [ ] Implement migration runner
- [ ] Add health check endpoint
- [ ] Set up connection metrics

### Phase 2.3: Service Layer
- [ ] Implement `SessionService`
  - [ ] CreateSession
  - [ ] GetSession
  - [ ] UpdateSession
  - [ ] ClaimNextPendingSession
  - [ ] FindOrphanedSessions
- [ ] Implement `StageService`
  - [ ] CreateStageExecution
  - [ ] UpdateStageExecution
  - [ ] GetStageExecutions
- [ ] Implement `InteractionService`
  - [ ] CreateLLMInteraction
  - [ ] CreateMCPInteraction
  - [ ] GetSessionTimeline
- [ ] Implement `ChatService`
  - [ ] CreateChat
  - [ ] AddChatMessage
  - [ ] GetChatHistory

### Phase 2.4: Testing
- [ ] Set up testcontainers integration
- [ ] Write service layer unit tests
- [ ] Write integration tests
- [ ] Test migration rollback scenarios
- [ ] Load testing with realistic data

### Phase 2.5: Documentation
- [ ] Generate Ent schema documentation
- [ ] Write service layer API docs
- [ ] Document migration procedures
- [ ] Create runbook for operations

---

## Open Questions for Discussion

1. **Session Locking**: Should we add pessimistic locking for concurrent updates to the same session (e.g., multiple pods racing)?

2. **Soft Deletes**: Do we want soft deletes (deleted_at field) or hard deletes with retention policy?

3. **Audit Trail**: Should we add an audit log table for tracking all modifications to sessions?

4. **Read Replicas**: Should we design for read replica support from the start (separate read/write clients)?

5. **Partitioning**: At what scale should we consider table partitioning by date?

6. **Event Cleanup**: What's the retention policy for the events table (used for WebSocket catchup)?

---

## Next Steps

After approval of this design:

1. Create Ent schema definitions
2. Generate initial migration
3. Set up podman-compose for local dev
4. Implement core service layer
5. Write comprehensive tests
6. Integrate with Phase 1 POC code

---

## References

- [Ent Documentation](https://entgo.io/docs/getting-started)
- [Atlas Migrations](https://atlasgo.io/)
- [PostgreSQL Connection Pooling Best Practices](https://www.postgresql.org/docs/current/runtime-config-connection.html)
- Old TARSy Database Implementation: `/home/igels/Projects/AI/tarsy-bot/backend/tarsy/`
