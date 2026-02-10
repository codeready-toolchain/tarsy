# Phase 4.1: MCP Client Infrastructure — Open Questions

**Status**: ✅ All Questions Resolved
**Last Updated**: 2026-02-09
**Related**: `docs/phase4-mcp-client-infrastructure-design.md` (updated to reflect all decisions)

---

## Q1: Where Should ActionInput Parameter Parsing Live?

**Status**: ✅ Decided — **Option A**
**Source**: Phase 3.2 deferred notes, `docs/phase4-open-questions.md`

**Decision:** In `MCPToolExecutor.Execute()`, calling `ParseActionInput()` from `pkg/mcp/params.go`. The MCPToolExecutor is the natural adapter between text-world (controllers) and structured-world (MCP SDK). Parsing logic lives in a pure function so it's testable regardless.

---

## Q2: Where Should Tool Name `server.tool` Validation Live?

**Status**: ✅ Decided — **Option C**
**Source**: Phase 3.2 deferred notes, `docs/phase4-open-questions.md`

**Decision:** Both layers. Parser keeps the loose dot-check (current behavior). Executor does strict regex + server existence validation. Two-tier approach: parser catches obvious garbage (`"kubectl"` → "must be server.tool"), executor catches edge cases (`.tool`, `server.`, `a.b.c` → specific routing error), controller's tool-name lookup catches unknown tools (LLM sees available tools list).

---

## Q3: NativeThinking Tool Name Format — Who Normalizes?

**Status**: ✅ Decided — **Option A**
**Source**: New design (no old TARSy equivalent — old TARSy used Python `server__tool` in the LLM client)

**Decision:** Controller normalizes. NativeThinking controller replaces `.` → `__` when passing tools to the LLM. The executor's `NormalizeToolName()` reverses it transparently on the way back. Controller already knows it's Gemini-specific; executor stays format-agnostic.

---

## Q4: Per-Session vs Shared MCP Client for HTTP/SSE Transports

**Status**: ✅ Decided — **Option A**
**Source**: New architecture decision (significant departure from old TARSy)

**Decision:** Per-session for all transports. Simple, complete isolation, no shared state. Go's `http.Client` handles HTTP connection pooling internally, so the per-session overhead for HTTP/SSE is just the MCP `Initialize` handshake. Can revisit if overhead becomes measurable in production.

---

## Q5: MCP Client Initialization Failure Policy

**Status**: ✅ Decided — **Option C**
**Source**: New design decision

**Decision:** Fail-open with pre-flight check. Initialize partially, warn the LLM in the system prompt about unavailable servers. The change is trivial — `appendMCPInstructions` already iterates over server IDs; adding a "## Unavailable MCP Servers" section for failed servers is a few lines. `MCPToolExecutor.FailedServers()` exposes the map, prompt builder includes it in the system message.

---

## Q6: Tool Result Error Handling — Go Error vs ToolResult.IsError

**Status**: ✅ Decided — **Option A**
**Source**: Interface design decision

**Decision:** MCP errors → `ToolResult{IsError: true}` (observable by LLM as observations). Go errors → `error` return (infrastructure failures: context cancelled, nil pointer). Matches MCP SDK convention and old TARSy behavior.

---

## Q7: Health Monitor Lifecycle — Eager vs Lazy Client Creation

**Status**: ✅ Decided — **Eager + fatal at startup, ongoing monitoring after**
**Source**: New design decision

**Decision:** Two-layer approach:

1. **Startup**: Eager MCP initialization. If any configured server fails to connect, TARSy does not become ready — the readiness probe fails. This catches broken configs and bugs in new deployments before they take traffic. Slower startup is acceptable because TARSy runs in OpenShift/K8s with rolling updates (no downtime during deploys).

2. **Runtime**: MCPHealthMonitor runs background checks (every 15s) to detect degradation after startup. Unhealthy servers surface as warnings in the dashboard (same as old TARSy).

This is stricter than old TARSy (which logged warnings on startup failures but continued) and provides better deployment safety.

---

## Q8: `TransportConfig.Env` — Map vs Inherited Environment

**Status**: ✅ Decided — **Option A**
**Source**: Config design (new field needed)

**Decision:** Inherit + override. Start with parent env (`os.Environ()`), add/override from config `Env` map. Subprocess gets `PATH`, `HOME`, etc. automatically. Matches old TARSy behavior and user expectations.

---

## Q9: MCP SDK Version — v1.2.0 Stable vs Latest

**Status**: ✅ Decided — **Option A**
**Source**: Dependency decision

**Decision:** Use v1.3.0 (latest stable, released Feb 2026).

---

## Q10: Tool Cache Invalidation Strategy

**Status**: ✅ Decided — **Option A**
**Source**: Performance optimization

**Decision:** Cache per client-instance lifetime, never invalidate. `ListTools` is only called once per agent execution (not per iteration), and each session/chat gets a fresh `MCPClient` (per Q4), so the cache is naturally short-lived (minutes). No invalidation logic needed.

---

## Q11: MCPToolExecutor Close() — Interface Compatibility

**Status**: ✅ Decided — **Option B**
**Source**: Interface design

**Decision:** Add `Close() error` to `ToolExecutor` interface. Compile-time safety, explicit cleanup. `StubToolExecutor` gets a no-op `Close()`. No backwards-compatibility concern — new TARSy is in active development, not yet in use.

---

## Previously Deferred Questions

Q1 and Q2 were originally captured in `docs/phase4-open-questions.md` (Phase 3.2 deferred notes). That file has been deleted — both questions are fully resolved above.
