# Phase 3.3: Prompt System — Design Questions

This document contains questions and concerns about the proposed Phase 3.3 architecture that need discussion before finalizing the design.

**Status**: ✅ All Decided  
**Created**: 2026-02-09  
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

### Q1: PromptBuilder as Concrete Struct vs Interface?

**Status**: ✅ Decided — **Option A: Concrete Struct**

**Context:**

The design proposes `PromptBuilder` as a concrete struct (not an interface). Old TARSy's `PromptBuilder` is also a concrete class. However, Go commonly uses interfaces for testability and dependency inversion.

#### Decision

Concrete struct. The prompt builder is deterministic and pure — given the same inputs, it always produces the same output. No side effects, no I/O. Tests can use the real builder with a test-configured MCP registry. If we need prompt variants in the future, we can introduce an interface then (easy refactor since all access is through `ExecutionContext.PromptBuilder`).

**Rejected alternative:**
- Option B (interface + default implementation) — unnecessary ceremony for a pure builder; large interface surface is a Go anti-pattern

---

### Q2: AlertType and RunbookContent — Where Do They Come From?

**Status**: ✅ Decided — **Option A: Executor Populates from DB + Config Defaults**

**Context:**

The design adds `AlertType` and `RunbookContent` fields to `ExecutionContext`. In new TARSy, `AlertType` is stored in `AlertSession.AlertType` (set at session creation from the alert webhook). `RunbookContent` is not yet fetched from GitHub (Phase 6); only `DefaultRunbook` exists in `BuiltinConfig`.

#### Decision

Executor populates both fields into `ExecutionContext`:
- `AlertType`: Read from `AlertSession.AlertType` (already available)
- `RunbookContent`: Use `BuiltinConfig.DefaultRunbook` as placeholder until Phase 6 adds actual runbook fetching

This follows the same pattern as `AlertData` — executor handles data fetching, prompt builder handles formatting. Clean separation keeps the builder pure and testable.

**Rejected alternative:**
- Option B (builder reads from DB/config directly) — makes the builder impure, violates stateless design principle, harder to test

---

### Q3: Alert Data Formatting — Try JSON Pretty-Print or Always Opaque Text?

**Status**: ✅ Decided — **Pass as-is, wrapped in HTML comment boundaries**

**Context:**

Old TARSy's `AlertSectionTemplate` assumes `alert_data` is JSON and calls `json.dumps(indent=2)`. New TARSy's database schema stores `AlertData` as `Text` (opaque string — could be JSON, YAML, or plain text).

#### Decision

Alert data is opaque text — it can be anything. Pass it as-is to the LLM without any parsing or reformatting. Wrap it in HTML comment boundaries for clear delimitation:

```
### Alert Data
<!-- ALERT_DATA_START -->
{raw alert data exactly as received}
<!-- ALERT_DATA_END -->
```

HTML comment boundaries are the established pattern in TARSy's prompts (runbook uses `<!-- RUNBOOK START/END -->`, stage context uses `<!-- STAGE_CONTEXT_START/END -->`, investigation results use `<!-- Investigation Result START/END -->`). They are extremely unlikely to appear in alert data, LLMs understand them as boundaries, and they don't modify the content. This is more robust than code fences, which could theoretically appear in alert payloads.

**Rejected alternatives:**
- Option A (try JSON pretty-print with fallback) — makes assumptions about data format, may reformat whitespace
- Option B (raw text without wrapping) — no clear delimiter for where alert data starts/ends
- Option C (code fence wrapping) — code fences could theoretically appear in alert data, causing collision

---

## ⚡ Important (Design Decisions)

### Q4: MCP Summarization Prompts — Include in Phase 3.3 or Defer to Phase 4?

**Status**: ✅ Decided — **Option A: Include in Phase 3.3**

**Context:**

MCP result summarization is triggered when a tool result exceeds `SummarizationConfig.SizeThresholdTokens`. The actual summarization call happens in Phase 4's MCP tool execution flow, but the *prompts* are part of the prompt system.

#### Decision

Include the MCP summarization prompt templates and builder methods in Phase 3.3. All prompt text lives in `pkg/agent/prompt/` from the start. Phase 4 just calls the builder methods — no prompt work needed. The templates are simple, standalone, and testable in isolation.

**Note:** These methods will not be called until Phase 4 (MCP Integration) implements the tool result summarization flow.

**Rejected alternative:**
- Option B (defer to Phase 4) — would mix prompt concerns into an already complex phase, and scatter prompt code across phases

---

### Q5: Executive Summary Prompts — Include in Phase 3.3 or Defer?

**Status**: ✅ Decided — **Option A: Include in Phase 3.3**

**Context:**

After an investigation completes, old TARSy generates a brief executive summary for alert notifications. This is a separate LLM call with its own system/user prompts.

#### Decision

Include the executive summary prompt templates in Phase 3.3. Same rationale as Q4 — all prompt text lives in `pkg/agent/prompt/`.

**Note:** These methods will not be called until Phase 5 (Session Completion → Executive summary generation).

**Rejected alternative:**
- Option B (defer to notification phase) — would scatter prompt code across phases

---

### Q6: Tool List Timing — Should ReAct BuildMessages Receive Pre-Listed Tools or List Them Internally?

**Status**: ✅ Decided — **Option A: Receive Tools as Parameter**

**Context:**

The design proposes that controllers list tools first, then pass the tool list to `BuildReActMessages()`. An alternative is that the prompt builder calls `ListTools()` internally.

#### Decision

Receive tools as a parameter. The prompt builder stays pure (no I/O, no `context.Context`). Controllers already call `ListTools()` for tool name validation (`buildToolNameSet`), so passing the result to the builder avoids a duplicate call and keeps a single source of truth for the tool list.

**Rejected alternative:**
- Option B (builder calls ListTools internally) — makes builder impure, requires mock ToolExecutor in tests, redundant ListTools call

---

### Q7: ChatContext.ChatHistory Type — []ConversationMessage vs Dedicated ChatExchange Type?

**Status**: ✅ Decided — **Option B: Introduce ChatExchange Type**

**Context:**

Currently `ChatContext.ChatHistory` is `[]ConversationMessage` (flat list of role/content pairs). Old TARSy uses a `ChatExchange` dataclass that groups a user question with its full conversation.

#### Decision

Introduce a `ChatExchange` type that groups a user question with its complete conversation:

```go
type ChatExchange struct {
    UserQuestion string
    Messages     []ConversationMessage
}

type ChatContext struct {
    UserQuestion        string
    InvestigationContext string
    ChatHistory         []ChatExchange
}
```

Chat exchanges are logically grouped (question -> conversation), and the prompt builder needs clean exchange boundaries for formatting "Exchange 1", "Exchange 2" sections. The executor already knows exchange boundaries when loading from the database.

**Rejected alternative:**
- Option A (flat `[]ConversationMessage`) — no grouping of exchanges, formatter has to infer boundaries from role transitions via heuristics

---

## 🔧 Implementation Details

### Q8: Parameter Ordering in extractParameters — Deterministic or Map Order?

**Status**: ✅ Decided — **Option A: Sort Parameters Alphabetically**

**Context:**

`extractParameters()` iterates over a `map[string]any` (from JSON Schema `properties`). Go maps have non-deterministic iteration order.

#### Decision

Sort parameters alphabetically. Deterministic output makes testing straightforward and consistent prompts are better for LLM behavior. The alphabetical order is neutral and predictable.

**Rejected alternatives:**
- Option B (accept map order) — non-deterministic across restarts, harder to test
- Option C (preserve JSON Schema order) — requires custom ordered map parsing, significantly more complexity for minor benefit

---

### Q9: Where Should FormatInvestigationContext Live?

**Status**: ✅ Decided — **Option B: In `pkg/agent/context/` (new file `investigation_formatter.go`)**

**Context:**

`FormatInvestigationContext()` converts timeline events from the original investigation into a formatted string for chat sessions.

#### Decision

Place it in `pkg/agent/context/investigation_formatter.go` alongside the existing `formatter.go`. The function transforms timeline events into text — that's exactly what the `context` package does. It already has the Ent dependency via `SimpleContextFormatter`, and both formatters serve the same purpose: converting DB data into text for prompts.

**Rejected alternatives:**
- Option A (`pkg/agent/prompt/chat.go`) — would create a prompt → Ent dependency; function is called by services before the prompt builder is involved
- Option C (`pkg/services/`) — formatting logic in a service feels misplaced; services shouldn't know about prompt formatting details

---

### Q10: Should Synthesis Use a Different Analysis Task Than Investigation Agents?

**Status**: ✅ Decided — **Option A: Keep Separate Tasks**

**Context:**

The design proposes separate analysis task instructions for investigation (`analysisTask`) and synthesis (`synthesisTask`).

#### Decision

Keep separate task instructions. Investigation and synthesis have fundamentally different goals — investigation agents gather data with tools and analyze, synthesis agents combine pre-gathered results into a unified analysis. Matches old TARSy's separate templates.

**Rejected alternative:**
- Option B (same task template) — investigation and synthesis goals are meaningfully different; a shared template would be too generic for either

---

## 📝 Minor / Cosmetic

### Q11: Section Separator Style — Unicode Box Drawing vs Simple Equals

**Status**: ✅ Decided — **Option A: Keep Unicode (═══)**

Keep Unicode box-drawing separators matching old TARSy. Visually distinctive, well-tested with LLMs, no encoding issues in practice.

**Rejected alternative:**
- Option B (ASCII `===`) — less visually distinct, no practical benefit

---

### Q12: Prompt Builder Package Name — `prompt` vs `prompts`?

**Status**: ✅ Decided — **Option A: `prompt` (singular)**

Go convention is singular package names (`fmt`, `net`, `sync`, `strings`). The package is `pkg/agent/prompt/`.

**Rejected alternative:**
- Option B (`prompts` plural) — doesn't follow Go naming conventions
