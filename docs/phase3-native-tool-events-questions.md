# Phase 3.2.1: Gemini Native Tool Timeline Events — Design Questions

This document contains questions and concerns about the proposed Phase 3.2.1 architecture that need discussion before finalizing the design.

**Status**: ✅ All Questions Decided  
**Created**: 2026-02-08  
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

### Q1: Proto Design — Single GroundingDelta vs Separate Messages for Google Search and URL Context

**Status**: ✅ Decided — **Option A: Single `GroundingDelta`**

**Context:**

The Gemini API returns grounding results in a single `GroundingMetadata` structure. Both Google Search and URL Context results share this structure:
- **Google Search**: Has `webSearchQueries`, `searchEntryPoint`, `groundingChunks`, `groundingSupports`
- **URL Context**: Has `groundingChunks` (and possibly `groundingSupports`) but **no** `webSearchQueries`

**Question:** Should we use one proto message or two?

#### Decision

Single `GroundingDelta` covering both Google Search and URL Context. Go determines the type based on `len(WebSearchQueries) > 0`. The shared structure is intentional — they come from the same Gemini API field (`GroundingMetadata`). The runtime detection is trivial and well-documented.

**Rejected alternatives:**
- Option B (separate `GoogleSearchDelta` and `UrlContextDelta`) — nearly identical field sets, doubles proto/Go surface area for no practical benefit, doesn't match Gemini API's data model

---

### Q2: Timeline Event Types — Dedicated Types vs Generic `native_tool_result` with Metadata

**Status**: ✅ Decided — **Option A: Dedicated event types**

**Context:**

The design proposes three new event types: `code_execution`, `google_search_result`, `url_context_result`. An alternative is a single generic `native_tool_result` type with a `metadata.tool_type` field.

**Question:** Should we use dedicated event types or a generic type?

#### Decision

Three dedicated enum values in the Ent schema: `code_execution`, `google_search_result`, `url_context_result`. Consistent with the existing pattern (we already have separate `llm_tool_call`, `mcp_tool_call`, `tool_result` rather than a generic "tool_event"). Native tool results have fundamentally different content structures (code vs URLs vs text-source mappings) that warrant separate types.

**Rejected alternatives:**
- Option B (generic `native_tool_result` with `metadata.tool_type`) — requires checking both `event_type` AND metadata for queries/rendering, no type safety for tool_type values, breaks the established pattern of specific event types

---

## ⚠️ Important (Design Clarification)

### Q3: URL Context Results — Are They Only in GroundingMetadata?

**Status**: ✅ Decided — **Option A: GroundingMetadata only**

**Context:**

The URL Context tool (`url_context`) fetches web page content and makes it available to the model. The design assumes URL Context results appear exclusively in `GroundingMetadata` (same structure as Google Search, differentiated by absence of `web_search_queries`).

**Question:** Do we need to capture URL Context results beyond what `GroundingMetadata` provides?

#### Decision

Capture URL Context results from `GroundingMetadata` only. The model's inline text references are already captured as `TextDelta`. Start with the confirmed and documented structure. If we discover additional URL Context data during testing with real Gemini calls, the proto `GroundingDelta` can easily be extended with new fields without breaking changes.

**Rejected alternatives:**
- Option B (investigate and capture additional URL Context data) — premature; start simple and extend if needed

---

### Q4: Code Execution Pairing — How to Match executable_code with code_execution_result

**Status**: ✅ Decided — **Option A: Pair in Go controller helpers**

**Context:**

Gemini streams code execution as separate parts: `executable_code` (code) then `code_execution_result` (output). The Python provider yields these as separate `CodeExecutionDelta` messages. The Go `collectStream` appends them to `LLMResponse.CodeExecutions`.

**Question:** Where should code/result pairing happen?

#### Decision

Pair in Go controller helpers. `createCodeExecutionEvents` iterates through `CodeExecutions`, pairing consecutive code+result entries with edge case handling. The order (code then result) is guaranteed by Gemini's API: the model generates code, executes it, then gets the result — they always come in pairs in sequence. This keeps both Python and `collectStream` as simple pass-throughs.

**Rejected alternatives:**
- Option B (pair in Python before streaming) — adds buffering complexity to Python, may not work across streaming chunks, loses part granularity for debugging
- Option C (pair in `collectStream`) — mixes collection and interpretation concerns, adds complexity for a specific use case

---

### Q5: Grounding Content Format — JSON vs Markdown in Timeline Event Content Field

**Status**: ✅ Decided — **Option B: Human-readable content + structured metadata**

**Context:**

Grounding results contain structured data (search queries, source URIs, text-to-source mappings). Timeline event content is typically human-readable text across all other event types.

**Question:** What format should grounding event content use?

#### Decision

Human-readable content with structured data in metadata. Consistent with all other event types (content is always human-readable). Cross-stage context formatting works without special-casing grounding events. Follows the pattern of `llm_tool_call` events (human-readable content + structured metadata). Frontend uses metadata for rich rendering (inline citations, clickable links).

- Content: `"Google Search: 'UEFA Euro 2024 winner' → Sources: UEFA.com (https://...), aljazeera.com (https://...)"`
- Metadata: `{"source": "gemini", "queries": [...], "sources": [...], "supports": [...]}`

**Rejected alternatives:**
- Option A (JSON content) — breaks human-readable content convention, requires special-casing in cross-stage context formatters, not useful when browsing DB directly
- Option C (minimal summary content + full metadata) — content alone not useful enough for cross-stage context

---

### Q6: Search Entry Point HTML — Where to Store and Should We?

**Status**: ✅ Decided — **Option C: Don't store — skip for now**

**Context:**

Grounding with Google Search includes a "Search Suggestions" widget using rendered HTML/CSS provided in `searchEntryPoint.renderedContent`. This can be a substantial HTML string (several KB).

**Question:** Should we store the search entry point HTML, and if so, where?

#### Decision

Skip storing the search entry point HTML for now. Simplest implementation with no storage overhead. Can be revisited later if ToS compliance becomes a concern.

**Rejected alternatives:**
- Option A (store in event metadata) — adds several KB per grounding event, only needed for frontend rendering
- Option B (store in LLMInteraction.response_metadata) — requires separate API call, complicates frontend

---

## 📋 Design Clarification

### Q7: Should ReActController Ever Create Native Tool Events?

**Status**: ✅ Decided — **Revised to Option A: ReAct does not create native tool events, but logs a warning if data is present**

**Context:**

ReActController uses the `langchain` backend where native tools are not exposed. However, the Phase 3.2 LangChain stub delegates to GoogleNativeProvider, and LangChain may expose native tools in the future.

#### Decision

Only NativeThinkingController and SynthesisController create native tool events (they use the `google-native` backend where native tools are expected). ReActController does **not** create native tool events — native tools are a `google-native` concern, and adding them to ReAct muddies the design boundary.

If native tool data unexpectedly appears in a ReAct response (via stub delegation or config error), a warning is logged. The data is still available in `LLMInteraction.response_metadata` for debugging.

Rationale for revision (from original Option B):
- The LangChain stub is temporary (Phase 3.2 only, replaced by real LangChain in Phase 6)
- "Future LangChain native tools" is speculative — if it happens, Phase 6 would redesign the controller anyway
- Adding native tool handling to ReAct creates conceptual overhead ("why does ReAct handle native tools?")
- YAGNI — solve it when it's actually a problem

**Rejected alternatives:**
- Option B (defensive — all controllers create native tool events) — adds conceptual complexity and maintenance burden to every controller for a scenario that's temporary (stub) or speculative (future LangChain)

---

### Q8: Multiple Grounding Events Per Response — Possible?

**Status**: ✅ Decided — **Option A: Support slice but expect single entry**

**Context:**

The Gemini API typically returns a single `GroundingMetadata` per response (at the candidate level). The design uses `[]GroundingChunk`.

#### Decision

Keep the slice type. Consistent with `CodeExecutions`, forward-compatible if Gemini ever supports multiple grounding entries, and the iteration loop is trivial regardless.

**Rejected alternatives:**
- Option B (pointer `*GroundingChunk`) — clearer "zero or one" semantics but inconsistent with `CodeExecutions` and breaks if Gemini adds multi-grounding support

---

## Summary

| Question | Topic | Priority | Recommendation |
|---|---|---|---|
| Q1 | Proto design — single vs separate grounding messages | ✅ Decided | Option A: Single `GroundingDelta` |
| Q2 | Event types — dedicated vs generic | ✅ Decided | Option A: Dedicated types (code_execution, google_search_result, url_context_result) |
| Q3 | URL Context results beyond GroundingMetadata | ✅ Decided | Option A: GroundingMetadata only, extend later if needed |
| Q4 | Code execution pairing location | ✅ Decided | Option A: Pair in Go controller helpers |
| Q5 | Grounding content format | ✅ Decided | Option B: Human-readable content + structured metadata |
| Q6 | Search entry point HTML storage | ✅ Decided | Option C: Don't store — skip for now |
| Q7 | ReAct native tool events | ✅ Decided | Revised: Option A — ReAct does not create events, logs warning if data present |
| Q8 | Multiple grounding events per response | ✅ Decided | Option A: Support slice but expect single entry |
