# Phase 3.1: Base Agent Architecture - Design Questions

This document contains questions and concerns about the proposed Phase 3.1 architecture that need discussion before finalizing the design.

**Status**: ✅ All Decided  
**Created**: 2026-02-07  
**Purpose**: Surface architectural decisions where the new TARSy significantly departs from old TARSy, or where non-obvious trade-offs need discussion

---

## How to Use This Document

For each question:
1. ✅ = Decided
2. 🔄 = In Discussion  
3. ⏸️ = Deferred
4. ❌ = Rejected

Add your answers inline under each question, then we'll update the main design doc.

---

## 🔥 Critical Priority (Architecture Decisions)

### Q1: LangChain Elimination — Native SDKs Only?

**Status**: ✅ Decided — **Option B: Keep LangChain for multi-provider + native SDK for Gemini thinking**

**Context:**

Old TARSy uses **two Python LLM clients**:
- **LangChain client** (`client.py`) — for all ReAct flows across all providers (OpenAI, Google, XAI, Anthropic, VertexAI). Provides unified streaming, tool binding, message conversion, and token usage tracking.
- **Gemini native client** (`gemini_client.py`) — for Gemini native thinking only. Uses raw `google.genai.Client`. Required because LangChain doesn't expose Gemini's thinking features (ThinkingConfig, thought signatures, thinking content).

**Question:** Should we use native SDKs for all providers, or keep LangChain for multi-provider abstraction?

**Rejected alternatives:**
- Option A (native SDKs only) — rejected because it requires reimplementing what LangChain already provides (streaming, message conversion, tool binding) for each provider. More work for Phase 6 with no clear benefit given old TARSy's proven LangChain usage.
- Option C (LangChain now, migrate later) — rejected because it creates technical debt and two migration efforts.

#### Decision

**Core principle:** New TARSy must provide the same functionality as old TARSy. Old TARSy uses this exact dual-client pattern and it's proven in production. We preserve it.

**Two clients required now:**

1. **`LangChainProvider`** — Uses LangChain for multi-provider abstraction. Handles streaming, tool binding, message conversion, and token usage tracking across all supported providers (Google, OpenAI, Anthropic, XAI, VertexAI). Used for all iteration strategies that don't require Gemini-specific thinking features: ReAct, synthesis, chat.

2. **`GoogleNativeProvider`** — Uses `google-genai` SDK directly. Required for Gemini-specific thinking features that LangChain doesn't expose: thinking content, thought signatures, ThinkingConfig (thinking budgets/levels). Used exclusively for native-thinking and synthesis-native-thinking iteration strategies.

**Provider routing:**

Go determines which backend to use based on the iteration strategy (Go drives iteration and knows what features it needs). A `backend` field in the proto `LLMConfig` lets Go signal to Python which provider implementation to use:

```protobuf
message LLMConfig {
  // ... existing fields ...
  string backend = N;  // "langchain" (default), "google-native", etc.
}
```

Go's config resolution sets the backend based on iteration strategy:
- `react`, `synthesis`, `chat` → `"langchain"`
- `native-thinking`, `synthesis-native-thinking` → `"google-native"`

Python servicer routes to the matching provider. If `backend` is empty/unset, defaults to `"langchain"`.

**Extensible design — provider registry pattern:**

The `LLMProvider` ABC stays as the common interface. Concrete providers register themselves with a provider registry. Adding a new backend in the future requires:
1. Implement a new class that extends `LLMProvider`
2. Register it with a new backend identifier
3. Set that identifier in the Go config where needed

No refactoring of existing code, no proto changes (the `backend` field is a free-form string).

```
llm-service/
├── llm/
│   ├── server.py
│   ├── servicer.py              # Routes to providers via registry
│   ├── providers/
│   │   ├── base.py              # LLMProvider ABC
│   │   ├── registry.py          # Provider registry (backend_name → LLMProvider)
│   │   ├── langchain_provider.py  # LangChain multi-provider
│   │   ├── google_native.py     # Gemini native thinking (google-genai SDK)
│   │   └── (future: openai_native.py, litellm_provider.py, etc.)
```

**Examples of future extensions (no redesign needed):**
- `AnthropicNativeProvider` — if Claude extended thinking needs native SDK access (similar to Gemini thinking)
- `OpenAINativeProvider` — if OpenAI native features are needed that LangChain doesn't expose
- `LiteLLMProvider` — alternative multi-provider abstraction (replaces or supplements LangChain)

**Phasing:**
- **Phase 3.1:** `GoogleNativeProvider` only (single provider for e2e validation, already partially built). Provider interface and registry designed for extensibility.
- **Phase 3.2:** `LangChainProvider` added (needed for ReAct controllers which don't use native thinking).
- **Phase 6:** Additional providers configured through LangChain or as new native implementations.

**Impact on design doc:**
- Python service architecture section: update to show dual-provider model instead of one-provider-per-SDK
- Provider interface (`base.py`): unchanged — `LLMProvider` ABC stays the same
- Proto `LLMConfig`: add `backend` field for provider routing
- Dependencies: `pyproject.toml` will need `langchain`, `langchain-google-genai`, `langchain-openai`, `langchain-anthropic`, etc. (added incrementally per phase)

---

### Q2: ReAct Text Parsing — Go or Python?

**Status**: ✅ Decided — **Option A: Parse in Go**

**Context:**

The Phase 3.1 design states that Go drives all iteration loops, including ReAct. This means Go must parse LLM text responses to extract tool calls from ReAct-style output:

```
Thought: I need to check the pod status
Action: kubernetes-server.resources_get
Action Input: {"resource_type": "pods", "namespace": "production"}
```

Old TARSy has a ReAct parser (`agents/parsers/react_parser.py`, ~500 lines) with multi-tier parsing, edge case handling, and multi-format parameter parsing.

**Question:** Should ReAct text parsing live in Go or Python?

**Rejected alternatives:**
- Option B (parse in Python, return structured data) — rejected because it makes Python aware of iteration strategies, couples it to TARSy's ReAct format, and requires Python to have tool context for validation. Violates the "thin proxy" principle.
- Option C (start simple, port incrementally) — rejected as a separate option; the full parser should be ported from the start to maintain feature parity with old TARSy. However, the phased implementation approach (Phase 3.2 scope) still applies.

#### Decision

**ReAct parsing lives entirely in Go.** This is orchestration logic — it determines what happens next (execute tool vs. return answer vs. handle error). Keeping it in Go maintains the clean Go/Python boundary.

**Key finding that removes the main risk:** The old TARSy ReAct parser does NOT parse streaming/partial output. There are two completely separate concerns:

1. **Streaming UI detection** (during token streaming): Simple substring checks (`"Thought:" in text`, `"Final Answer:" in text`) to decide what to show in the UI. Trivial in Go (`strings.Contains()`). In new TARSy, Go receives streaming chunks from Python via gRPC and can do these checks as tokens accumulate.

2. **Full ReAct parsing** (after stream completes): `ReActParser.parse_response()` is called **once per iteration with the complete response text**. This is a line-by-line state machine — straightforward to implement in Go.

**What the Go parser must implement** (ported from old TARSy's `react_parser.py`):

1. **Section extraction** — line-by-line state machine tracking current section (`thought`, `action`, `action_input`, `final_answer`) with content accumulation
2. **3-tier section detection:**
   - Tier 1: Standard format (`line.startswith("Action:")`)
   - Tier 2: Mid-line Final Answer detection after sentence boundary (`r'[.!?][\s]*Final Answer:'`)
   - Tier 3: Fallback for Action/Action Input mid-line after sentence boundary
3. **Missing Action recovery** — backtrack from `Action Input:` to find `Action:` when LLM omits newlines
4. **Tool name validation** — `server.tool` format with regex `^[\w\-]+\.[\w\-]+$`
5. **Parameter parsing** — JSON (primary), YAML, comma-separated `key: value`, `key=value` formats
6. **Stop conditions** — hallucinated observation detection (`Observation:`, `[Based on...]`)
7. **Error feedback** — specific format error messages based on which sections were found/missing
8. **Continuation prompts** — format correction reminders for malformed responses

**Go is well-suited for this:**

| Python feature | Go equivalent |
|---|---|
| `line.startswith("Action:")` | `strings.HasPrefix(line, "Action:")` |
| `re.search(pattern, text)` | `regexp.MustCompile(pattern).FindString(text)` |
| `text.split('\n')` | `strings.Split(text, "\n")` |
| `json.loads(input)` | `json.Unmarshal([]byte(input), &result)` |
| `yaml.safe_load(input)` | `yaml.Unmarshal([]byte(input), &result)` |
| Section state tracking | Typed enum + switch |

Estimated ~400-600 lines of Go. Highly testable with table-driven tests (many edge cases = many test rows).

**Data flow in new TARSy:**

```
Go builds messages → Python streams response via gRPC → Go accumulates text
  ├── During streaming: Go does simple substring checks for UI events (trivial)
  └── After stream complete: Go calls ReAct parser on full text (state machine)
```

**Proto impact:** `GenerateResponse` streams raw text via `TextDelta`. Go accumulates and parses. No "ReAct mode" or pre-parsed tool calls from Python needed. This keeps the proto clean and Python unaware of iteration strategies.

**Phasing:**
- **Phase 3.1:** No parser needed (SingleCallController doesn't iterate)
- **Phase 3.2:** Full Go ReAct parser implemented as part of ReActController, ported from old TARSy's `react_parser.py`

**Go package location:** `pkg/agent/parser/` — separate from controllers so it can be unit tested independently.

---

### Q3: Provider-Specific Features Through the Proto

**Status**: ✅ Decided — **Option A (enhanced): Minimal proto + code execution as metadata**

**Context:**

The Phase 3.1 proto is designed to be provider-agnostic. But several Gemini-specific features don't map cleanly to the generic proto:

1. **Thought signatures** — opaque binary blob for multi-turn reasoning continuity (native thinking only)
2. **Google native tools** — `google_search`, `code_execution`, `url_context` (mutually exclusive with MCP function calling)
3. **Code execution results** — `executable_code` and `code_execution_result` parts returned by Gemini

**Question:** How should the proto handle provider-specific features?

**Rejected alternatives:**
- Option B (opaque `provider_state` blob round-tripped through Go) — rejected because it adds proto complexity for a problem that Python in-memory state already solves. Python and Go share the same pod/container lifecycle, so Python restart = Go restart = session re-queued. No practical risk.
- Option C (explicit `thought_signature` proto field) — rejected for the same reason. Adds Gemini-specific fields to the proto when in-memory caching is sufficient.

#### Decision

Three sub-concerns, each handled differently:

**1. Native tools configuration** — Already solved. `LLMConfig.native_tools` map (existing proto field). Go knows the agent configuration and what provider/strategy it's using. Go sets `native_tools` based on config. Python passes them to the Gemini API. Go being aware of provider specifics is fine — it knows exactly what client/provider it's using. Mutual exclusivity with MCP tools is covered in Q6.

**2. Thought signatures** — Python stores in memory, keyed by `execution_id` (not `session_id` — a session has multiple stages and parallel agents, each with its own independent conversation). Entries cleaned up after 1 hour TTL (well above typical agent execution duration). This matches old TARSy's pattern (old TARSy also stores thought signatures in Python memory via `GeminiNativeThinkingClient`). Acceptable because:
- Python and Go live in the same pod/container with the same lifecycle
- If the pod restarts, Go's session executor also restarts — the session would be re-queued
- Thinking continuity degradation is graceful (LLM works without it, just loses some reasoning context from previous turns)
- Only relevant for native-thinking strategy (Phase 3.2), not Phase 3.1

**3. Code execution results** — Exposed as structured metadata through the proto, not hidden in Python. When `code_execution` is enabled, Gemini returns `executable_code` and `code_execution_result` parts. These are intermediate reasoning artifacts (the model writes code, executes it, incorporates results into its response). They should be visible to Go for:
- Storage in LLMInteraction records (debugging/observability)
- Optional streaming to frontend (investigation transparency)
- Parity with old TARSy which stores them in metadata

Add a new `CodeExecutionDelta` to the `GenerateResponse` oneof, consistent with the existing delta pattern:

```protobuf
message GenerateResponse {
  oneof content {
    TextDelta text = 1;
    ThinkingDelta thinking = 2;
    ToolCallDelta tool_call = 3;
    UsageInfo usage = 4;
    ErrorInfo error = 5;
    CodeExecutionDelta code_execution = 6;  // NEW
  }
  bool is_final = 10;
}

message CodeExecutionDelta {
  string code = 1;    // The generated Python code
  string result = 2;  // Execution output (stdout/stderr)
}
```

Code execution can happen multiple times in one response (model iterates: writes code → gets result → writes more code). Streaming them as deltas preserves ordering and lets Go handle them as they arrive.

**Proto impact summary:**
- `LLMConfig.native_tools` — already exists, no change
- `GenerateResponse` — add `CodeExecutionDelta` to the oneof (field reserved now, Python implementation when code execution is built)
- No `provider_state` or `thought_signature` fields needed — Python handles thought signatures in memory

**Phasing and affected documents:**

- **Phase 3.1:** Reserve `CodeExecutionDelta` in proto. No thought signature or code execution implementation yet (SingleCallController doesn't iterate, code execution not in scope).
- **Phase 3.2 (native thinking controller):** Implement thought signature caching in `GoogleNativeProvider`. The native thinking controller in Go uses `backend: "google-native"` — Python's `GoogleNativeProvider` manages thought signatures internally per `session_id`.
  - **Update:** `phase3-base-agent-architecture-design.md` — Python service section should note thought signature caching in `GoogleNativeProvider`.
- **Phase 3.2 (iteration controllers):** Code execution results handled by controllers that support native tools (e.g., ReAct with no MCP tools). Go receives `CodeExecutionDelta`, stores in LLMInteraction.
  - **Update:** Phase 3.2 design doc (when created) — document `CodeExecutionDelta` handling in controllers.
- **Phase 3.4 (real-time streaming):** Code execution deltas can be streamed to frontend for investigation transparency.
  - **Update:** Phase 3.4 design — include `CodeExecutionDelta` in WebSocket event protocol.
- **Phase 6 (multi-LLM):** If other providers add code execution features (e.g., Anthropic artifacts), `CodeExecutionDelta` is already available in the proto.
  - **Update:** Phase 6 design — document per-provider code execution mapping.

---

## 📋 High Priority

### Q4: Python Client Lifecycle — Per-Request or Cached?

**Status**: ✅ Decided — **Option A: Cache clients at startup (like old TARSy)**

**Context:**

Old TARSy's `LLMManager` creates and caches LLM client instances at startup. New TARSy's Python service is "stateless per-request" for conversation data, but this raises the question: should the underlying SDK client objects also be created per-request or cached?

**Question:** How should Python manage LLM SDK client instances?

**Rejected alternatives:**
- Option B (fresh client per-request) — rejected because all major LLM providers explicitly recommend against it. Per-request creation loses connection pooling (new TCP/TLS handshake per call), repeats authentication (Google: "multiple seconds" per auth), and can trigger auth rate limiting. With up to 100 LLM calls per session, this would severely degrade performance.
- Option C (cache per API key with TTL) — rejected as unnecessary complexity for Phase 3.1. If API key rotation becomes a requirement, this is a backwards-compatible optimization.

#### Decision

Cache SDK client instances at startup, matching old TARSy's `LLMManager` pattern. Provider instances are created once during service initialization and reused across all gRPC requests.

**Why caching is critical** (confirmed by provider documentation):

- **Google genai SDK**: First request performs auth that takes "multiple seconds." Token cached per client instance (~1 hour, auto-refreshes). Creating too many clients can trigger auth rate limiting.
- **OpenAI SDK** (httpx): Per-request creation "defeats the purpose of connection pooling." Default pool: 20 keep-alive connections.
- **Anthropic SDK** (httpx): Same — creating per-request destroys pool when function returns.
- **LangChain**: Wraps the above SDKs, so same principles apply.

**Implementation:**

```python
class LLMServicer:
    def __init__(self):
        self.registry = ProviderRegistry()
        # Providers created once at startup with cached SDK clients
        self.registry.register("langchain", LangChainProvider())
        self.registry.register("google-native", GoogleNativeProvider())

    async def Generate(self, request, context):
        provider = self.registry.get(request.llm_config.backend)
        async for chunk in provider.generate(request.messages, ...):
            yield chunk
```

**API key changes require service restart.** This is consistent with Go's "restart on config change" pattern from Phase 2 — configuration is read at startup, not hot-reloaded.

**"Stateless per-request" clarification:** The Python service is stateless regarding *conversation data* (each `GenerateRequest` is self-contained). Client caching is infrastructure state (connection pools, auth tokens), not application state — same as a database connection pool.

---

### Q5: Error Handling and Retry Strategy — Go, Python, or Both?

**Status**: ✅ Decided — **Option C: Both — Python handles transient, Go handles strategic**

**Context:**

Old TARSy has comprehensive retry logic in Python: 3 retries for rate limits (429) with exponential backoff, retries for empty responses, timeout protection (120s default), and consecutive timeout failure tracking.

**Question:** Where does retry/error handling live?

**Rejected alternatives:**
- Option A (retry in Python only) — rejected because Go loses control over retry decisions. If a session is cancelled or timed out while Python is retrying, Go can't intervene (though gRPC context cancellation mitigates this, the architectural concern remains — Go should own strategic decisions).
- Option B (retry in Go only) — rejected because each retry becomes a full gRPC round-trip. Transient issues like rate limits are best handled close to the error source. Go shouldn't need to know about 429s that resolve after a 1-second backoff.

#### Decision

Clear two-layer retry strategy. Each layer handles what it knows best.

**Python handles transient retries (automatic, hidden from Go):**
- Rate limit errors (429): up to 3 retries, exponential backoff (or provider-suggested `Retry-After` delay). Matches old TARSy behavior.
- Empty responses: up to 3 retries, 3s delay. Matches old TARSy behavior.
- If Python's retries are exhausted, it returns `ErrorInfo(retryable=true)` so Go can make a higher-level decision.
- Non-retryable errors (auth failures, invalid requests, etc.) returned immediately as `ErrorInfo(retryable=false)`.

**Go handles strategic retries and overall control:**
- If Python returns `ErrorInfo(retryable=true)`: Go can retry the entire gRPC call, try a different provider, or fail the iteration based on session-level retry budget.
- Consecutive timeout/failure tracking: Go tracks consecutive failures across iterations (like old TARSy's `consecutive_timeout_failures`) and stops the loop after threshold (e.g., 2 consecutive timeouts).
- Provider fallback: Go can switch providers if one is consistently failing (Phase 6).

**Go retains full control via gRPC context — timeouts and cancellation cut through Python's retry loop:**

- **Timeouts**: Go sets deadline on the gRPC context (`context.WithTimeout`). When it expires, gRPC cancels the call on both sides. Python's retry loop, backoff sleep, and in-flight LLM calls are all interrupted immediately.
- **Manual cancellation**: Go cancels the context (e.g., user cancels session via API). Same mechanism — instant propagation. Python's `context.cancelled()` becomes true, retry loop aborts.
- **No blocking concern**: Even `asyncio.sleep()` during Python's backoff is interrupted by gRPC context cancellation. Go is never waiting on Python longer than its own timeout allows.

**Boundary summary:**

| Concern | Layer | Behavior |
|---|---|---|
| Rate limit (429) | Python | Retry up to 3x with backoff, hidden from Go |
| Empty response | Python | Retry up to 3x with 3s delay, hidden from Go |
| Timeout per LLM call | Go | `context.WithTimeout` on gRPC call, cancels Python |
| Session cancellation | Go | `context.Cancel` propagates through gRPC to Python |
| Consecutive failures | Go | Track across iterations, stop loop after threshold |
| Provider failover | Go | Retry with different provider (Phase 6) |
| Auth/config errors | Python | Return immediately as `ErrorInfo(retryable=false)` |

---

### Q6: Gemini Native Tools vs MCP Tools — Mutual Exclusivity

**Status**: ✅ Decided — **Option A: Python handles it silently (like old TARSy)**

**Context:**

Gemini's API has a hard limitation: you cannot combine native Google tools (`google_search`, `code_execution`, `url_context`) with function calling (MCP tools) in the same API call. Old TARSy handles this by prioritizing MCP tools when both are present.

**Question:** How should Go/Python handle the mutual exclusivity of Gemini native tools and MCP function calling?

**Rejected alternatives:**
- Option B (Go handles it explicitly) — rejected because it puts Gemini-specific API knowledge in Go. Python is the layer that talks to the Gemini API and understands the constraint.
- Option C (configuration-level validation) — rejected because it's too restrictive. Different stages in a chain may legitimately use native tools (no MCP) in one stage and MCP tools (no native) in another.

#### Decision

Python enforces the mutual exclusivity rule, matching old TARSy's proven behavior:
- If `tools` (MCP function definitions) is non-empty → ignore `native_tools` from `LLMConfig`, use only MCP function calling
- If `tools` is empty → use `native_tools` as configured
- Log a warning when `native_tools` are suppressed due to MCP tools being present

This is the right layer because Python is the one making the Gemini API call and understands the provider constraint. Go sends both `native_tools` (via `LLMConfig`) and `tools` (MCP definitions) based on configuration — Python resolves the conflict at call time.

---

## 📊 Medium Priority

### Q7: Tool Name Format — Consistent Naming

**Status**: ✅ Decided — **Option C: Proto uses canonical name, conversion is per-provider**

**Context:**

Old TARSy uses different tool name formats per strategy: `server.tool` (dot) for ReAct, `server__tool_name` (double underscore) for native thinking. The difference exists because Gemini's function calling API doesn't allow dots in function names.

**Question:** What tool name format should we use in the proto?

**Rejected alternatives:**
- Option A (double underscore everywhere) — rejected because it sacrifices readability across the entire system for a single provider's restriction. Less readable in logs, proto messages, and Go code.
- Option B (dot in proto, Python converts for Gemini) — not rejected per se, but generalized into Option C. Same approach, but C makes the conversion responsibility per-provider rather than Gemini-specific.

#### Decision

`ToolDefinition.name` in the proto uses the canonical readable format: `server.tool` (e.g., `kubernetes-server.resources_get`). Go always works with this format — in the ReAct parser, in conversation messages, in MCP tool execution, in logs.

Each Python provider converts to/from its required format internally:
- `GoogleNativeProvider`: converts `server.tool` → `server__tool` when sending to Gemini function calling API, and `server__tool` → `server.tool` when returning `ToolCallDelta` to Go
- `LangChainProvider`: same conversion for Gemini via LangChain; other providers may not need conversion
- Future providers: handle their own naming restrictions in their own code

Provider-specific naming restrictions are implementation details, not proto concerns. The conversion is simple (bidirectional string replace of `.` ↔ `__`) and lives in the provider layer.

---

### Q8: Streaming Chunk Granularity and UI Responsiveness

**Status**: ✅ Decided — **Forward SDK chunks as-is**

**Context:**

Old TARSy uses fine-grained streaming with different chunk sizes per content type (1-3 tokens between updates). The Python provider receives SDK streaming chunks and must decide how to forward them via gRPC.

**Question:** What streaming granularity should the Python service use?

**Rejected alternative:**
- Option B (buffer to consistent size) — rejected because it adds latency and complexity for a problem that may not exist. Hard to choose a universal buffer size, and the frontend can handle smoothing if needed.

#### Decision

Forward SDK streaming chunks as-is — each SDK chunk becomes a `GenerateResponse` proto message. No buffering in Python. Simplest implementation, no added latency.

SDK streaming is already reasonable (providers don't send 1-byte chunks). If we observe UI issues (inconsistent experience across providers, too-small chunks causing gRPC overhead), we can add buffering later — it's a backwards-compatible optimization in the Python provider layer.

---

## 📝 Summary Checklist

Track which questions we've addressed:

### Critical Priority
- [x] Q1: LangChain Elimination — **Option B: Keep LangChain + native Gemini SDK** (dual-provider with registry pattern)
- [x] Q2: ReAct Text Parsing — **Option A: Parse in Go** (state machine on complete text, not streaming)
- [x] Q3: Provider-Specific Features — **Option A (enhanced):** native tools via LLMConfig, thought signatures in Python memory, code execution as `CodeExecutionDelta`

### High Priority
- [x] Q4: Python Client Lifecycle — **Option A: Cache at startup** (all providers recommend reuse for connection pooling and auth caching)
- [x] Q5: Error Handling and Retry Strategy — **Option C: Both** (Python: transient retries; Go: timeouts, cancellation, strategic retries)
- [x] Q6: Gemini Native Tools vs MCP Tools — **Option A: Python handles silently** (MCP tools present → ignore native tools, log warning)

### Medium Priority
- [x] Q7: Tool Name Format — **Option C: Canonical `server.tool` in proto**, per-provider conversion (e.g., `server__tool` for Gemini)
- [x] Q8: Streaming Chunk Granularity — **Forward SDK chunks as-is** (optimize later if needed)

---

## Next Steps

1. Go through each question in order
2. Add answers inline under each question
3. Mark status (✅ Decided / ❌ Rejected / ⏸️ Deferred)
4. Update the Phase 3.1 design document based on decisions
