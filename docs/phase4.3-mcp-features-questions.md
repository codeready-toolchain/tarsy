# Phase 4.3: MCP Features — Open Questions

**Status**: ✅ All Questions Resolved
**Last Updated**: 2026-02-09
**Related**: `docs/phase4.3-mcp-features-design.md`

---

## Q1: Where Should Summarization Logic Live?

**Status**: ✅ Decided — **Option A**
**Source**: Significant architecture departure from old TARSy

**Decision:** Controller level. Summarization happens in the controller after `ToolExecutor.Execute()` returns via a shared `maybeSummarize()` function called from both ReAct and NativeThinking controllers. This keeps `ToolExecutor` focused on MCP execution while the controller (which already has LLMClient, conversation context, EventPublisher, and services) handles LLM orchestration. The conversation context requirement is the decisive factor — injecting it into ToolExecutor or a decorator adds complexity for no functional benefit.

---

## Q2: Summarization LLM — Same Model or Dedicated?

**Status**: ✅ Decided — **Option A**
**Source**: Performance and cost consideration

**Decision:** Same LLM as the agent (`execCtx.Config.LLMProvider`). Zero configuration, same model quality for understanding technical output. The summarization prompt is small and the result is bounded by `summary_max_token_limit`, so costs are acceptable. Phase 8 (Multi-LLM Support) is the natural place to add a dedicated summarization model option if needed.

---

## Q3: How to Handle the Summarization Timeline Event Type?

**Status**: ✅ Decided — **Option A**
**Source**: New streaming pattern for non-LLM-response events

**Decision:** Separate streaming function. Create `callSummarizationLLMWithStreaming()` that creates `mcp_tool_summary` events with server/tool metadata. The summarization variant is simpler than `callLLMWithStreaming` (~80–90 lines vs ~160) because it has no thinking stream and only one event type. Structural overlap is ~40–50 lines. This avoids touching `callLLMWithStreaming` which is critical path code. If a third streaming use case emerges, refactor to extract a shared helper (Option C).

---

## Q4: Should MCP Override Validation Happen at API Time or Execution Time?

**Status**: ✅ Decided — **Option A**
**Source**: Error reporting timing

**Decision:** Both layers. Validate at API time (immediate 400 response for unknown servers) AND at execution time (defensive check in `resolveMCPSelection`). The API validation is trivial (one `registry.Has()` call per server) and provides immediate feedback. The execution-time check is defense in depth against config changes between submission and execution.

---

## Q5: Conversation Context Size for Summarization Prompt

**Status**: ✅ Decided — **Option A**
**Source**: Context window management

**Decision:** Full conversation (minus system prompt). Include all messages to give the summarizer maximum investigation context. The summarization model (Q2: same as agent) already handles large context windows (Gemini models support 1M+ tokens). The tool result being summarized is the large part of the input, not the conversation context. Simplest implementation — no truncation logic needed.

---

## Q6: How Should Large Tool Results Be Truncated?

**Status**: ✅ Decided
**Source**: Interaction between summarization, storage, and `MaxToolResultTokens`

**Decision:** Two independent truncation concerns:

1. **Storage truncation** (UI/DB protection) — Always truncate raw results stored in `llm_tool_call` completion content and MCPInteraction records. Lower threshold. Applies to ALL results regardless of whether summarization is triggered. Protects the dashboard from rendering massive text blobs.

2. **Summarization input safety net** — When feeding the summarization LLM, truncate to a larger limit (model's context window minus prompt overhead). The summarizer should get as much data as possible for quality, but bounded as a safety net.

No separate conversation truncation for non-summarized results. If a result is below the summarization threshold, it's already small enough for the conversation. If summarization is disabled, that's a deliberate choice. With Gemini's 1M+ context window, `MaxToolResultTokens` as a conversation-level hard cap is unnecessary. Summarization *is* the mechanism for controlling result size in the conversation.

```
Raw MCP result (masked)
  ├─ Store truncated version → llm_tool_call completion + MCPInteraction (lower limit, UI-safe)
  ├─ If summarization triggered:
  │     ├─ Safety-net truncate → summarization LLM input (larger limit)
  │     └─ Summary → agent conversation
  └─ If NOT summarized:
        └─ Full result → agent conversation (small enough by definition)
```

---

## Q7: NativeTools Override — Where to Apply?

**Status**: ✅ Decided — **Option A**
**Source**: New capability from MCP selection config

**Decision:** Store `NativeToolsOverride` in `ResolvedAgentConfig`. Controllers merge the override with the provider's native tools config when building `GenerateInput`. Implementation is straightforward (merge three optional bool pointers for tri-state: nil=provider default, true=enable, false=disable). The NativeThinking controller already reads native tools from provider config; adding a merge step is minimal.

---

## Q8: Tool Call Event Model — Separate Events or Single-Event Lifecycle?

**Status**: ✅ Decided — **Single-event lifecycle on `llm_tool_call`**
**Source**: Event storage trade-off, dashboard consumption simplicity

**Decision:** Reuse the existing `llm_tool_call` timeline event with a streaming lifecycle (same pattern as `llm_response`). One event per tool call, two states:

- **Created** (status: `streaming`): content="", metadata={server_name, tool_name, arguments}. Dashboard shows spinner.
- **Completed** (status: `completed`): content=storage-truncated raw result, metadata enriched with {is_error}. Dashboard shows result.

This eliminates both `mcp_tool_call` (no new event type needed) and `tool_result` (raw result lives on the completed `llm_tool_call`). Arguments move from content to metadata so they survive the content update on completion. No schema migration needed for the event type enum.

The decisive factor is dashboard simplicity: one event ID per tool call, no multi-event correlation, no race conditions on reconnect, reliable catchup from DB. Additional metadata can be attached on completion if different components need to contribute data.
