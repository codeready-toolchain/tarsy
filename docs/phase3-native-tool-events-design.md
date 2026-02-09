# Phase 3.2.1: Gemini Native Tool Timeline Events — Detailed Design

**Status**: 🔵 Design Phase  
**Last Updated**: 2026-02-08

## Overview

This document details the implementation of timeline events for Gemini native tools (`google_search`, `code_execution`, `url_context`). These tools produce results inline in the Gemini response stream that need to be surfaced to users via timeline events.

**Phase 3.2.1 Scope**: Proto additions, Python streaming, Go chunk types, Ent schema event types, controller logic, and helper functions to create timeline events for native tool results.

**Key Design Principles:**
- Native tool results become first-class timeline events (improvement over old TARSy)
- Reuse existing streaming infrastructure (proto deltas → Go chunks → `collectStream`)
- Controllers create events after response collection (Phase 3.2 buffered pattern)
- Native tools are mutually exclusive with MCP tools (handled in Python, decided in Phase 3.1 Q6)
- Only relevant for `google-native` backend strategies

**What This Phase Delivers:**
- Proto: `GroundingDelta` message for Google Search and URL Context grounding results
- Python: Extraction and streaming of `GroundingMetadata` from Gemini response
- Go: `GroundingChunk` type, `LLMResponse.Groundings` field, `collectStream` update
- Ent schema: Three new timeline event types (`code_execution`, `google_search_result`, `url_context_result`)
- Controller helpers: Functions to create native tool timeline events
- NativeThinkingController and SynthesisController updates

**What This Phase Does NOT Deliver:**
- WebSocket streaming of native tool events to frontend (Phase 3.4)
- Frontend rendering of native tool timeline events (Phase 10)
- URL Context tool-specific result extraction beyond grounding metadata (see Q3)

---

## Current State

### What Exists Today

| Component | State | Details |
|---|---|---|
| Config | ✅ Complete | `google_search`, `code_execution`, `url_context` enums in `pkg/config/enums.go` |
| Proto | ✅ Partial | `CodeExecutionDelta` message exists; no grounding messages |
| Python | ✅ Partial | Streams `CodeExecutionDelta` for `executable_code`/`code_execution_result` parts; enables native tools when no MCP tools present; **does NOT extract `GroundingMetadata`** |
| Go LLM Client | ✅ Partial | `CodeExecutionChunk` type exists; `collectStream` collects into `LLMResponse.CodeExecutions` |
| Go Controllers | ❌ Missing | Code executions stored in `LLMInteraction.response_metadata` only (debugging) — no timeline events created |
| Ent Schema | ❌ Missing | No `code_execution`, `google_search_result`, or `url_context_result` event types |

### What Old TARSy Does

Old TARSy captures native tool results **only in `LLMInteraction.response_metadata`** (visible on the dashboard but not surfaced as dedicated timeline events):
- **Code execution**: Parts stored in `response_metadata['parts']` array, filtered out during streaming to preserve ReAct format
- **Google Search**: Grounding metadata captured in `response_metadata['grounding_metadata']` but not surfaced as timeline events
- **URL Context**: Same as Google Search — stored in grounding metadata but not surfaced

**New TARSy improvement**: Native tool results become first-class timeline events, visible in the investigation timeline alongside LLM thinking, tool calls, and final analysis.

---

## Architecture

### Data Flow

```
Gemini API Response
│
├── executable_code part ──────────► CodeExecutionDelta (proto)
│                                    └► CodeExecutionChunk (Go)
│                                       └► LLMResponse.CodeExecutions
│
├── code_execution_result part ────► CodeExecutionDelta (proto)
│                                    └► CodeExecutionChunk (Go)
│                                       └► LLMResponse.CodeExecutions
│
├── GroundingMetadata ─────────────► GroundingDelta (proto) ◄── NEW
│   ├── webSearchQueries               └► GroundingChunk (Go) ◄── NEW
│   ├── searchEntryPoint                   └► LLMResponse.Groundings ◄── NEW
│   ├── groundingChunks
│   └── groundingSupports
│
└── text/thinking/tool_call parts ─► (existing deltas, unchanged)

                    ▼ After collectStream ▼

NativeThinkingController / SynthesisController
│
├── CodeExecutions present? ──────► Create code_execution timeline events
├── Groundings present? ──────────► Create google_search_result or
│                                   url_context_result timeline events
└── (existing logic unchanged)
```

### Which Controllers Create Native Tool Events

| Controller | Native Tool Events? | Reason |
|---|---|---|
| NativeThinkingController | Yes | Uses `google-native` backend; native tools available when no MCP tools |
| SynthesisController | Yes (synthesis-native-thinking only) | Uses `google-native` backend; synthesis never has MCP tools |
| ReActController | Yes (defensive) | Uses `langchain` backend; native tools not expected but handled defensively (Q7) |
| SingleCallController | No | Phase 3.1 validation only, not production |

**Edge case: NativeThinkingController forced conclusion** — When forcing a conclusion, the controller calls LLM **without tools** (`Tools: nil`). If the agent's config had native tools enabled, they could theoretically activate during this tool-less call. The forced conclusion logic already uses `callLLM` → `collectStream` → the existing pipeline, so native tool events would be captured naturally.

---

## Proto Changes

### New Message: GroundingDelta

A single `GroundingDelta` message covers both Google Search grounding and URL Context grounding. They share the same underlying structure (`GroundingMetadata` from the Gemini API), differentiated by which fields are populated:
- **Google Search**: Has `web_search_queries` populated
- **URL Context**: Has `grounding_chunks` but no `web_search_queries`

Go determines the source type when creating timeline events.

```protobuf
// GroundingDelta carries grounding metadata from Gemini.
// Covers both Google Search grounding and URL Context grounding.
// Sent as a late delta (after content parts, before UsageInfo).
//
// Google Search: has web_search_queries populated
// URL Context: has grounding_chunks but no web_search_queries
//
// Go determines the source type when creating timeline events.
message GroundingDelta {
  // Search queries used by the model (Google Search only).
  // Empty for URL Context grounding.
  repeated string web_search_queries = 1;

  // Web sources referenced by the model.
  repeated GroundingChunkInfo grounding_chunks = 2;

  // Text segments supported by grounding sources.
  // Links model response text to specific grounding_chunks.
  repeated GroundingSupport grounding_supports = 3;

  // Rendered HTML/CSS for the Google Search widget (Google Search only).
  // Empty for URL Context.
  // NOTE: Streamed from Python but not stored in timeline events (Q6 decision — skip for now).
  string search_entry_point_html = 4;
}

// GroundingChunkInfo represents a single web source.
message GroundingChunkInfo {
  string uri = 1;    // Web source URL
  string title = 2;  // Page title
}

// GroundingSupport links a text segment to its grounding sources.
message GroundingSupport {
  int32 start_index = 1;                    // Start of text segment
  int32 end_index = 2;                      // End of text segment
  string text = 3;                          // The supported text
  repeated int32 grounding_chunk_indices = 4; // Indices into grounding_chunks
}
```

### Updated GenerateResponse

```protobuf
message GenerateResponse {
  oneof content {
    TextDelta text = 1;
    ThinkingDelta thinking = 2;
    ToolCallDelta tool_call = 3;
    UsageInfo usage = 4;
    ErrorInfo error = 5;
    CodeExecutionDelta code_execution = 6;
    GroundingDelta grounding = 7;             // NEW
  }

  bool is_final = 10;
}
```

### Why One Message Instead of Two

Using separate `GoogleSearchDelta` and `UrlContextDelta` messages was considered but rejected because:
1. They share the exact same structure (both come from `GroundingMetadata`)
2. The differentiation is which fields are populated, not different field sets
3. One message keeps the proto simpler and avoids two nearly-identical definitions
4. Go's determination logic is trivial: `len(delta.WebSearchQueries) > 0` → Google Search, otherwise URL Context

---

## Python Changes

### Extract GroundingMetadata

The `_stream_with_timeout` method in `GoogleNativeProvider` needs to extract `grounding_metadata` from the streaming response. According to the Gemini API, `grounding_metadata` is available on the candidate level, typically on the last chunk of a streaming response.

```python
# llm-service/llm/providers/google_native.py

async def _stream_with_timeout(self, ...):
    has_content = False
    last_usage = None
    last_grounding_metadata = None  # NEW: buffer grounding metadata

    try:
        async with asyncio.timeout(timeout_seconds):
            stream = await client.aio.models.generate_content_stream(...)
            async for chunk in stream:
                if not chunk.candidates:
                    # ... existing usage handling ...
                    continue

                candidate = chunk.candidates[0]

                # NEW: Capture grounding_metadata when available
                if hasattr(candidate, 'grounding_metadata') and candidate.grounding_metadata:
                    last_grounding_metadata = candidate.grounding_metadata

                if not candidate.content or not candidate.content.parts:
                    # ... existing usage handling ...
                    continue

                for part in candidate.content.parts:
                    # ... existing part handling (thinking, function_call,
                    #     executable_code, code_execution_result, text) ...

                # ... existing usage buffering ...

    except asyncio.TimeoutError as exc:
        raise _RetryableError(...)

    if not has_content:
        raise _RetryableError(...)

    # NEW: Yield grounding metadata after content (before usage)
    if last_grounding_metadata is not None:
        yield self._build_grounding_delta(last_grounding_metadata)

    # Yield buffered usage info
    if last_usage is not None:
        yield last_usage

    yield pb.GenerateResponse(is_final=True)


def _build_grounding_delta(self, gm) -> pb.GenerateResponse:
    """Convert Gemini GroundingMetadata to proto GroundingDelta."""
    delta = pb.GroundingDelta()

    # Web search queries
    if hasattr(gm, 'web_search_queries') and gm.web_search_queries:
        delta.web_search_queries.extend(gm.web_search_queries)

    # Grounding chunks (web sources)
    if hasattr(gm, 'grounding_chunks') and gm.grounding_chunks:
        for chunk in gm.grounding_chunks:
            if hasattr(chunk, 'web') and chunk.web:
                delta.grounding_chunks.append(
                    pb.GroundingChunkInfo(
                        uri=chunk.web.uri or "",
                        title=chunk.web.title or "",
                    )
                )

    # Grounding supports (text-source links)
    if hasattr(gm, 'grounding_supports') and gm.grounding_supports:
        for support in gm.grounding_supports:
            segment = support.segment if hasattr(support, 'segment') else None
            gs = pb.GroundingSupport(
                start_index=segment.start_index if segment else 0,
                end_index=segment.end_index if segment else 0,
                text=segment.text if segment and hasattr(segment, 'text') else "",
            )
            if hasattr(support, 'grounding_chunk_indices') and support.grounding_chunk_indices:
                gs.grounding_chunk_indices.extend(support.grounding_chunk_indices)
            delta.grounding_supports.append(gs)

    # Search entry point HTML — extracted for completeness but not stored in timeline events (Q6)
    if hasattr(gm, 'search_entry_point') and gm.search_entry_point:
        if hasattr(gm.search_entry_point, 'rendered_content'):
            delta.search_entry_point_html = gm.search_entry_point.rendered_content or ""

    return pb.GenerateResponse(grounding=delta)
```

### Ordering in the Stream

The stream chunk order after this change:

```
1. Content parts (text, thinking, function_call, executable_code, code_execution_result)
2. GroundingDelta (if grounding metadata present)          ◄── NEW
3. UsageInfo (if usage metadata present)
4. is_final=True
```

This matches the pattern for `UsageInfo` — metadata yielded after content is confirmed but before the final marker.

---

## Go Changes

### New Chunk Type

```go
// pkg/agent/llm_client.go

// GroundingChunk carries grounding metadata from the LLM response.
// Covers both Google Search grounding and URL Context grounding.
type GroundingChunk struct {
    WebSearchQueries     []string
    Sources              []GroundingSource
    Supports             []GroundingSupport
    SearchEntryPointHTML string // Populated from proto but not stored in timeline events (Q6)
}

// GroundingSource represents a web source referenced by the LLM.
type GroundingSource struct {
    URI   string
    Title string
}

// GroundingSupport links a text segment to its grounding sources.
type GroundingSupport struct {
    StartIndex          int
    EndIndex            int
    Text                string
    GroundingChunkIndices []int
}

const ChunkTypeGrounding ChunkType = "grounding"

func (c *GroundingChunk) chunkType() ChunkType { return ChunkTypeGrounding }
```

### Updated LLMResponse

```go
// pkg/agent/controller/helpers.go

type LLMResponse struct {
    Text           string
    ThinkingText   string
    ToolCalls      []agent.ToolCall
    CodeExecutions []agent.CodeExecutionChunk
    Groundings     []agent.GroundingChunk     // NEW
    Usage          *agent.TokenUsage
}
```

### Updated collectStream

```go
func collectStream(stream <-chan agent.Chunk) (*LLMResponse, error) {
    resp := &LLMResponse{}
    var textBuf, thinkingBuf strings.Builder

    for chunk := range stream {
        switch c := chunk.(type) {
        case *agent.TextChunk:
            textBuf.WriteString(c.Content)
        case *agent.ThinkingChunk:
            thinkingBuf.WriteString(c.Content)
        case *agent.ToolCallChunk:
            resp.ToolCalls = append(resp.ToolCalls, agent.ToolCall{
                ID:        c.CallID,
                Name:      c.Name,
                Arguments: c.Arguments,
            })
        case *agent.CodeExecutionChunk:
            resp.CodeExecutions = append(resp.CodeExecutions, agent.CodeExecutionChunk{
                Code:   c.Code,
                Result: c.Result,
            })
        case *agent.GroundingChunk:                    // NEW
            resp.Groundings = append(resp.Groundings, *c)
        case *agent.UsageChunk:
            resp.Usage = &agent.TokenUsage{
                InputTokens:    c.InputTokens,
                OutputTokens:   c.OutputTokens,
                TotalTokens:    c.TotalTokens,
                ThinkingTokens: c.ThinkingTokens,
            }
        case *agent.ErrorChunk:
            return nil, fmt.Errorf("LLM error: %s (code: %s, retryable: %v)",
                c.Message, c.Code, c.Retryable)
        }
    }

    resp.Text = textBuf.String()
    resp.ThinkingText = thinkingBuf.String()
    return resp, nil
}
```

### Updated GRPCLLMClient

The `fromProtoResponse` function in `llm_grpc.go` needs a new case:

```go
// pkg/agent/llm_grpc.go — in fromProtoResponse()

case *llmv1.GenerateResponse_Grounding:
    g := v.Grounding
    chunk := &agent.GroundingChunk{
        WebSearchQueries:     g.WebSearchQueries,
        SearchEntryPointHTML: g.SearchEntryPointHtml,
    }
    for _, gc := range g.GroundingChunks {
        chunk.Sources = append(chunk.Sources, agent.GroundingSource{
            URI:   gc.Uri,
            Title: gc.Title,
        })
    }
    for _, gs := range g.GroundingSupports {
        chunk.Supports = append(chunk.Supports, agent.GroundingSupport{
            StartIndex:            int(gs.StartIndex),
            EndIndex:              int(gs.EndIndex),
            Text:                  gs.Text,
            GroundingChunkIndices: intSliceFromInt32(gs.GroundingChunkIndices),
        })
    }
    return chunk
```

---

## Ent Schema Changes

### New Timeline Event Types

Add three new event types to the existing `TimelineEvent` schema:

```go
// ent/schema/timelineevent.go — update Values()

field.Enum("event_type").
    Values(
        "llm_thinking",
        "llm_response",
        "llm_tool_call",
        "tool_result",
        "mcp_tool_call",
        "mcp_tool_summary",
        "error",
        "user_question",
        "executive_summary",
        "final_analysis",
        "code_execution",         // NEW: Gemini code execution (code + result)
        "google_search_result",   // NEW: Google Search grounding (queries, sources)
        "url_context_result",     // NEW: URL Context grounding (sources)
    ),
```

### Event Type Semantics

| Event Type | Source | Content (human-readable) | Metadata (structured) |
|---|---|---|---|
| `code_execution` | Gemini `executable_code` + `code_execution_result` parts | `"```python\n{code}\n```\n\nOutput:\n```\n{result}\n```"` | `{"source": "gemini"}` |
| `google_search_result` | Gemini `GroundingMetadata` with `web_search_queries` | `"Google Search: 'query1', 'query2' → Sources: UEFA.com (https://...), aljazeera.com (https://...)"` | `{"source": "gemini", "queries": [...], "sources": [...], "supports": [...]}` |
| `url_context_result` | Gemini `GroundingMetadata` without `web_search_queries` | `"URL Context → Sources: example.com (https://...), docs.k8s.io (https://...)"` | `{"source": "gemini", "sources": [...], "supports": [...]}` |

### Content and Metadata Split for Grounding Events (Q5 Decision)

All timeline event content is human-readable text — consistent across every event type. Grounding events follow this convention:

- **Content**: Human-readable summary of the grounding (queries + source titles/URIs). Works for cross-stage context formatting, DB browsing, and fallback rendering.
- **Metadata**: Full structured data (queries, sources with URIs, grounding supports with text-to-source mappings, search entry point HTML). Frontend uses metadata for rich rendering (inline citations, clickable links, Google Search widget).

This follows the pattern established by `llm_tool_call` events: human-readable content (tool arguments) plus structured metadata (`tool_name`).

### Code Execution Content Format

Code execution events use a human-readable format (markdown-style code blocks) because:
- Code and output are sequential text, not structured data
- Easy to render in any frontend (even without special handling)
- Multiple code executions per response → multiple events, each self-contained

---

## Controller Changes

### Helper Functions

New helper functions in `pkg/agent/controller/helpers.go`:

```go
// createCodeExecutionEvents creates timeline events for Gemini code executions.
// Each executable_code + code_execution_result pair becomes a separate event.
// Returns the number of events created.
func createCodeExecutionEvents(
    ctx context.Context,
    execCtx *agent.ExecutionContext,
    codeExecutions []agent.CodeExecutionChunk,
    eventSeq *int,
) int {
    created := 0

    // Pair up code and result chunks.
    // Gemini streams them as: executable_code (code only), code_execution_result (result only).
    // The Python provider yields them as separate CodeExecutionDelta messages:
    //   {code: "...", result: ""} followed by {code: "", result: "..."}
    // We pair consecutive code+result into single timeline events.
    var pendingCode string
    for _, ce := range codeExecutions {
        if ce.Code != "" && ce.Result == "" {
            // This is an executable_code part — buffer the code
            if pendingCode != "" {
                // Previous code had no result — emit it alone
                content := formatCodeExecution(pendingCode, "")
                createTimelineEvent(ctx, execCtx, timelineevent.EventTypeCodeExecution,
                    content, map[string]interface{}{"source": "gemini"}, eventSeq)
                created++
            }
            pendingCode = ce.Code
        } else if ce.Result != "" && ce.Code == "" {
            // This is a code_execution_result — pair with pending code
            content := formatCodeExecution(pendingCode, ce.Result)
            createTimelineEvent(ctx, execCtx, timelineevent.EventTypeCodeExecution,
                content, map[string]interface{}{"source": "gemini"}, eventSeq)
            pendingCode = ""
            created++
        } else if ce.Code != "" && ce.Result != "" {
            // Both present (shouldn't happen with current Python, but handle gracefully)
            if pendingCode != "" {
                content := formatCodeExecution(pendingCode, "")
                createTimelineEvent(ctx, execCtx, timelineevent.EventTypeCodeExecution,
                    content, map[string]interface{}{"source": "gemini"}, eventSeq)
                created++
            }
            content := formatCodeExecution(ce.Code, ce.Result)
            createTimelineEvent(ctx, execCtx, timelineevent.EventTypeCodeExecution,
                content, map[string]interface{}{"source": "gemini"}, eventSeq)
            pendingCode = ""
            created++
        }
    }

    // Emit any remaining code without result
    if pendingCode != "" {
        content := formatCodeExecution(pendingCode, "")
        createTimelineEvent(ctx, execCtx, timelineevent.EventTypeCodeExecution,
            content, map[string]interface{}{"source": "gemini"}, eventSeq)
        created++
    }

    return created
}

// formatCodeExecution formats a code execution pair for timeline event content.
func formatCodeExecution(code, result string) string {
    var sb strings.Builder
    if code != "" {
        sb.WriteString("```python\n")
        sb.WriteString(code)
        sb.WriteString("\n```\n")
    }
    if result != "" {
        sb.WriteString("\nOutput:\n```\n")
        sb.WriteString(result)
        sb.WriteString("\n```")
    }
    return sb.String()
}

// createGroundingEvents creates timeline events for grounding results.
// Determines event type based on whether web_search_queries are present:
//   - With queries → google_search_result
//   - Without queries → url_context_result
// Content is human-readable; structured data goes in metadata (Q5 decision).
func createGroundingEvents(
    ctx context.Context,
    execCtx *agent.ExecutionContext,
    groundings []agent.GroundingChunk,
    eventSeq *int,
) int {
    created := 0

    for _, g := range groundings {
        if len(g.Sources) == 0 {
            continue // No sources — skip empty grounding
        }

        // Build structured metadata (full data for frontend rich rendering)
        metadata := map[string]interface{}{
            "source":  "gemini",
            "sources": formatGroundingSources(g.Sources),
        }
        if len(g.Supports) > 0 {
            metadata["supports"] = formatGroundingSupports(g.Supports)
        }

        var eventType timelineevent.EventType
        var content string

        if len(g.WebSearchQueries) > 0 {
            // Google Search grounding
            eventType = timelineevent.EventTypeGoogleSearchResult
            metadata["queries"] = g.WebSearchQueries
            content = formatGoogleSearchContent(g.WebSearchQueries, g.Sources)
        } else {
            // URL Context grounding
            eventType = timelineevent.EventTypeUrlContextResult
            content = formatUrlContextContent(g.Sources)
        }

        createTimelineEvent(ctx, execCtx, eventType, content, metadata, eventSeq)
        created++
    }

    return created
}

// formatGoogleSearchContent creates a human-readable summary for google_search_result events.
// Example: "Google Search: 'query1', 'query2' → Sources: UEFA.com (https://...), aljazeera.com (https://...)"
func formatGoogleSearchContent(queries []string, sources []agent.GroundingSource) string {
    var sb strings.Builder
    sb.WriteString("Google Search: ")
    for i, q := range queries {
        if i > 0 {
            sb.WriteString(", ")
        }
        sb.WriteString("'")
        sb.WriteString(q)
        sb.WriteString("'")
    }
    sb.WriteString(" → Sources: ")
    for i, s := range sources {
        if i > 0 {
            sb.WriteString(", ")
        }
        if s.Title != "" {
            sb.WriteString(s.Title)
            sb.WriteString(" (")
            sb.WriteString(s.URI)
            sb.WriteString(")")
        } else {
            sb.WriteString(s.URI)
        }
    }
    return sb.String()
}

// formatUrlContextContent creates a human-readable summary for url_context_result events.
// Example: "URL Context → Sources: example.com (https://...), docs.k8s.io (https://...)"
func formatUrlContextContent(sources []agent.GroundingSource) string {
    var sb strings.Builder
    sb.WriteString("URL Context → Sources: ")
    for i, s := range sources {
        if i > 0 {
            sb.WriteString(", ")
        }
        if s.Title != "" {
            sb.WriteString(s.Title)
            sb.WriteString(" (")
            sb.WriteString(s.URI)
            sb.WriteString(")")
        } else {
            sb.WriteString(s.URI)
        }
    }
    return sb.String()
}

// formatGroundingSources converts grounding sources to a serializable format for metadata.
func formatGroundingSources(sources []agent.GroundingSource) []map[string]string {
    result := make([]map[string]string, 0, len(sources))
    for _, s := range sources {
        result = append(result, map[string]string{
            "uri":   s.URI,
            "title": s.Title,
        })
    }
    return result
}

// formatGroundingSupports converts grounding supports to a serializable format for metadata.
func formatGroundingSupports(supports []agent.GroundingSupport) []map[string]interface{} {
    result := make([]map[string]interface{}, 0, len(supports))
    for _, s := range supports {
        result = append(result, map[string]interface{}{
            "start_index":             s.StartIndex,
            "end_index":               s.EndIndex,
            "text":                    s.Text,
            "grounding_chunk_indices": s.GroundingChunkIndices,
        })
    }
    return result
}
```

### NativeThinkingController Update

After collecting the LLM response and creating thinking/text/tool events, add native tool event creation:

```go
// In NativeThinkingController.Run() — after state.RecordSuccess()

// Record thinking content (existing)
if resp.ThinkingText != "" {
    createTimelineEvent(ctx, execCtx, timelineevent.EventTypeLlmThinking, ...)
}

// NEW: Create native tool events
createCodeExecutionEvents(ctx, execCtx, resp.CodeExecutions, &eventSeq)
createGroundingEvents(ctx, execCtx, resp.Groundings, &eventSeq)

// Check for tool calls (existing logic continues...)
```

The same pattern applies in the `forceConclusion` method:

```go
// In NativeThinkingController.forceConclusion() — after collecting resp

// Record thinking (existing)
if resp.ThinkingText != "" { ... }

// NEW: Create native tool events (can occur during forced conclusion too)
createCodeExecutionEvents(ctx, execCtx, resp.CodeExecutions, &eventSeq)
createGroundingEvents(ctx, execCtx, resp.Groundings, &eventSeq)

// Create final_analysis (existing)
createTimelineEvent(ctx, execCtx, timelineevent.EventTypeFinalAnalysis, ...)
```

### SynthesisController Update

Same pattern in `SynthesisController.Run()`:

```go
// In SynthesisController.Run() — after collecting resp

// Record thinking (existing)
if resp.ThinkingText != "" { ... }

// NEW: Create native tool events
createCodeExecutionEvents(ctx, execCtx, resp.CodeExecutions, &eventSeq)
createGroundingEvents(ctx, execCtx, resp.Groundings, &eventSeq)

// Create final_analysis (existing)
createTimelineEvent(ctx, execCtx, timelineevent.EventTypeFinalAnalysis, ...)
```

### ReActController Update (Q7 — Defensive)

ReActController uses the `langchain` backend where native tools are not normally exposed. However, native tool event creation is added defensively to prevent silent data loss if native tool results appear (e.g., via the Phase 3.2 LangChain stub delegating to GoogleNativeProvider, future LangChain native tool support, or config errors).

```go
// In ReActController.Run() — after collecting resp (in the iteration loop)

// Parse text for tool calls (existing ReAct logic)
parsed := parseReActResponse(resp.Text)

// Defensive: create native tool events if present in the response.
// ReAct uses the langchain backend where native tools are not normally exposed.
// However, the Phase 3.2 LangChain stub delegates to GoogleNativeProvider,
// and future LangChain versions may expose Gemini native tools.
// If collectStream captured any native tool data, surface it rather than
// silently discarding it.
createCodeExecutionEvents(ctx, execCtx, resp.CodeExecutions, &eventSeq)
createGroundingEvents(ctx, execCtx, resp.Groundings, &eventSeq)

// Continue with existing tool call / tool result logic...
```

### LLM Interaction Recording Update

Native tool data should also be recorded in `LLMInteraction.response_metadata` for debugging (matching old TARSy's pattern):

```go
// In recordLLMInteraction() — add native tool data to response metadata

llmResponseMeta := map[string]any{
    "text_length":      textLen,
    "tool_calls_count": toolCallsCount,
}

// Add code execution data if present
if resp != nil && len(resp.CodeExecutions) > 0 {
    var codeExecs []map[string]string
    for _, ce := range resp.CodeExecutions {
        codeExecs = append(codeExecs, map[string]string{
            "code":   ce.Code,
            "result": ce.Result,
        })
    }
    llmResponseMeta["code_executions"] = codeExecs
}

// Add grounding data if present
if resp != nil && len(resp.Groundings) > 0 {
    llmResponseMeta["groundings_count"] = len(resp.Groundings)
}
```

---

## Timeline Event Flow Per Iteration

### NativeThinkingController — with native tools

```
LLM call → collect stream → LLMResponse contains:
  ├── ThinkingText ──────────► llm_thinking event (existing)
  ├── CodeExecutions ────────► code_execution event(s) (NEW)
  ├── Groundings ────────────► google_search_result or url_context_result event(s) (NEW)
  ├── Text + ToolCalls ──────► llm_response + tool_call events (existing)
  │   └── tool execution ───► tool_result events (existing)
  └── Text only ─────────────► final_analysis event (existing)
```

### Event Ordering Within an Iteration

Events are created in this order after `collectStream` returns:

1. `llm_thinking` (if thinking content present)
2. `code_execution` (if code executions present) — NEW
3. `google_search_result` / `url_context_result` (if grounding present) — NEW
4. `llm_response` (if text alongside tool calls) OR `final_analysis` (if no tool calls)
5. `tool_call` + `tool_result` (for each tool call, if tool calls present)

This ordering reflects the logical flow: the model thinks, executes code, searches, then produces text and/or tool calls.

### Updated Event Types by Controller

| Event Type | ReAct | Native Thinking | Synthesis |
|---|---|---|---|
| `llm_response` | — | ✅ | — |
| `llm_thinking` | ✅ | ✅ | ✅ |
| `tool_call` | ✅ | ✅ | — |
| `tool_result` | ✅ | ✅ | — |
| `final_analysis` | ✅ | ✅ | ✅ |
| `error` | ✅ | ✅ | ✅ |
| `code_execution` | — | ✅ (NEW) | ✅ (NEW) |
| `google_search_result` | — | ✅ (NEW) | ✅ (NEW) |
| `url_context_result` | — | ✅ (NEW) | ✅ (NEW) |

---

## Testing Strategy

### Unit Tests

**Proto/Go chunk types:**
- `fromProtoResponse` correctly converts `GroundingDelta` → `GroundingChunk`
- `collectStream` collects `GroundingChunk` into `LLMResponse.Groundings`
- Multiple grounding chunks accumulated correctly
- Empty grounding (no sources) handled gracefully

**Helper functions:**
- `createCodeExecutionEvents` — pairs code+result correctly
- `createCodeExecutionEvents` — handles code-only (no result), result-only (unlikely), both
- `createCodeExecutionEvents` — multiple executions per response
- `formatCodeExecution` — produces expected markdown format
- `createGroundingEvents` — creates `google_search_result` when queries present
- `createGroundingEvents` — creates `url_context_result` when no queries
- `createGroundingEvents` — skips empty groundings (no sources)
- `formatGroundingSources` / `formatGroundingSupports` — correct JSON structure

**Controller integration:**
- NativeThinkingController creates code_execution events when code executions present
- NativeThinkingController creates google_search_result events when grounding present
- NativeThinkingController creates url_context_result events when URL context grounding present
- SynthesisController creates native tool events (synthesis-native-thinking path)
- ReActController creates native tool events defensively if data present (Q7)
- Events created in correct sequence order
- forceConclusion creates native tool events if present in response

**Python:**
- `_build_grounding_delta` correctly extracts `GroundingMetadata` fields
- Google Search grounding: `web_search_queries`, `grounding_chunks`, `grounding_supports` all populated; `search_entry_point` extracted but not stored in events (Q6)
- URL Context grounding: only `grounding_chunks` populated, no queries
- Grounding delta yielded after content, before usage
- Missing/empty `grounding_metadata` → no delta yielded
- Partial grounding metadata (some fields missing) → graceful handling

### Mock Data for Tests

```go
// Mock response with code execution
resp := &LLMResponse{
    Text: "The calculation shows...",
    CodeExecutions: []agent.CodeExecutionChunk{
        {Code: "print(2 + 2)", Result: ""},   // executable_code
        {Code: "", Result: "4"},                // code_execution_result
    },
}

// Mock response with Google Search grounding
resp := &LLMResponse{
    Text: "Spain won Euro 2024...",
    Groundings: []agent.GroundingChunk{
        {
            WebSearchQueries: []string{"UEFA Euro 2024 winner"},
            Sources: []agent.GroundingSource{
                {URI: "https://...", Title: "UEFA.com"},
            },
            Supports: []agent.GroundingSupport{
                {StartIndex: 0, EndIndex: 20, Text: "Spain won Euro 2024", GroundingChunkIndices: []int{0}},
            },
        },
    },
}
```

---

## Implementation Checklist

### Phase 3.2.1 Implementation Order

1. **Proto changes** (foundation for everything else):
   - [ ] Add `GroundingChunkInfo`, `GroundingSupport`, `GroundingDelta` messages to `proto/llm_service.proto`
   - [ ] Add `grounding` to `GenerateResponse` oneof (field 7)
   - [ ] Regenerate Go proto code
   - [ ] Regenerate Python proto code

2. **Python changes** (extract and stream grounding):
   - [ ] Add `_build_grounding_delta()` method to `GoogleNativeProvider`
   - [ ] Update `_stream_with_timeout()` to capture `candidate.grounding_metadata`
   - [ ] Yield `GroundingDelta` after content, before usage
   - [ ] Write Python unit tests for grounding extraction
   - [ ] Test with real Gemini API call (google_search enabled)

3. **Go LLM client changes** (receive grounding chunks):
   - [ ] Add `GroundingChunk`, `GroundingSource`, `GroundingSupport` types to `pkg/agent/llm_client.go`
   - [ ] Add `ChunkTypeGrounding` constant
   - [ ] Update `fromProtoResponse()` in `pkg/agent/llm_grpc.go` for `GroundingDelta`
   - [ ] Add `intSliceFromInt32` helper if needed
   - [ ] Write unit tests for proto conversion

4. **Stream collection changes**:
   - [ ] Add `Groundings` field to `LLMResponse` in `pkg/agent/controller/helpers.go`
   - [ ] Add `case *agent.GroundingChunk` to `collectStream()`
   - [ ] Write unit tests for `collectStream` with grounding chunks

5. **Ent schema changes** (new event types):
   - [ ] Add `code_execution`, `google_search_result`, `url_context_result` to timeline event type enum
   - [ ] Regenerate Ent code (`go generate ./ent`)
   - [ ] Run auto-migration

6. **Controller helper functions** (create events):
   - [ ] Implement `createCodeExecutionEvents()` in `pkg/agent/controller/helpers.go`
   - [ ] Implement `formatCodeExecution()` helper
   - [ ] Implement `createGroundingEvents()` in `pkg/agent/controller/helpers.go`
   - [ ] Implement `formatGroundingSources()`, `formatGroundingSupports()` helpers
   - [ ] Update `recordLLMInteraction()` to include code execution and grounding data in metadata
   - [ ] Write comprehensive unit tests for all helpers

7. **Controller updates** (wire in event creation):
   - [ ] Update `NativeThinkingController.Run()` — add native tool event creation after response collection
   - [ ] Update `NativeThinkingController.forceConclusion()` — add native tool event creation
   - [ ] Update `SynthesisController.Run()` — add native tool event creation
   - [ ] Write controller unit tests with mock native tool responses

---

## Design Decisions

### What Changed from Old TARSy

| Aspect | Old TARSy | New TARSy | Reason |
|---|---|---|---|
| Code execution display | Stored in `response_metadata` only (debugging); filtered from streaming | First-class `code_execution` timeline events | Users should see the model's code execution reasoning |
| Google Search grounding | Stored in `response_metadata` only; dashboard parsed metadata for display | First-class `google_search_result` timeline events with structured content | Grounding data is valuable user-facing information (sources, citations) |
| URL Context results | Same as Google Search — metadata only | First-class `url_context_result` timeline events | Consistency with Google Search treatment |
| Native tool event creation | N/A (no events) | Created in controllers after `collectStream` | Matches Phase 3.2 buffered pattern; clean separation |

### What Stayed the Same

- Code execution parts captured as `CodeExecutionDelta` in proto (already existed)
- Native tools suppressed when MCP tools present (Python handles, Phase 3.1 Q6)
- Native tool data also stored in `LLMInteraction.response_metadata` (debugging)
- Only relevant for `google-native` backend strategies

---

## References

- Phase 3.2 Design: `docs/phase3-iteration-controllers-design.md`
- Phase 3.1 Design: `docs/phase3-base-agent-architecture-design.md`
- Phase 3.1 Q3 (Provider-Specific Features): `docs/phase3-base-agent-architecture-questions.md`
- Phase 3.1 Q6 (Native Tools vs MCP): `docs/phase3-base-agent-architecture-questions.md`
- Gemini API Grounding docs: https://ai.google.dev/gemini-api/docs/grounding
- Current Proto: `proto/llm_service.proto`
- Current Python Provider: `llm-service/llm/providers/google_native.py`
- Current Go Helpers: `pkg/agent/controller/helpers.go`
- Old TARSy LLM Client: `/home/igels/Projects/AI/tarsy-bot/backend/tarsy/integrations/llm/client.py`
- Old TARSy Dashboard Parser: `/home/igels/Projects/AI/tarsy-bot/dashboard/src/utils/nativeToolsParser.ts`
