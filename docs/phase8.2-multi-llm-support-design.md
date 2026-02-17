# Phase 8.2: Multi-LLM Support — Design Document

## Overview

This phase replaces the LangChain stub with a real LangChain-based provider supporting OpenAI, Anthropic, xAI, Google (via LangChain), and VertexAI. It also **completely removes the ReAct iteration strategy** and replaces it with a new `langchain` strategy that uses native/structured tool calling via LangChain across all non-Gemini providers.

The Google Native provider (`google-native` backend) is kept for Gemini models via the existing `native-thinking` strategy — it offers unique features (Content caching for thought signatures, native tools like Google Search/URL Context/Code Execution) that LangChain cannot fully replicate.

### Key Decisions

1. **ReAct is dead.** All text-based `Thought:/Action:/Final Answer:` parsing is removed. All providers use native function calling.
2. **Two backends remain:** `google-native` (Gemini SDK) and `langchain` (all other providers + optionally Gemini via LangChain).
3. **Four iteration strategies:** `native-thinking` (kept, Gemini native SDK), `langchain` (new, all other providers), `synthesis` (kept, langchain backend), `synthesis-native-thinking` (kept, google-native backend). Only `react` is deleted.
4. **Maximize LangChain features:** streaming, native thinking/reasoning blocks (`content_blocks`), `bind_tools()` for function calling.
5. **Both `native-thinking` and `langchain` share the same controller** (renamed to `FunctionCallingController`) — the logic is identical (structured tool calls, thinking chunks, completion = no tool calls).
6. **Backend resolution stays strategy-based** — no auto-resolution, no heuristics. Each strategy explicitly maps to a backend.

---

## Architecture

### Before (Current State)

```
Iteration Strategies:
  react                      → ReActController           → langchain backend (stub → google-native)
  native-thinking            → NativeThinkingController   → google-native backend
  synthesis                  → SynthesisController        → langchain backend (stub → google-native)
  synthesis-native-thinking  → SynthesisController        → google-native backend
```

### After (Target State)

```
Iteration Strategies:
  native-thinking            → FunctionCallingController  → google-native backend
  langchain                  → FunctionCallingController  → langchain backend
  synthesis                  → SynthesisController        → langchain backend
  synthesis-native-thinking  → SynthesisController        → google-native backend
```

Every strategy explicitly maps to exactly one backend. No auto-resolution, no heuristics. The user has full control over which backend handles each agent, including synthesis.

---

## Component Changes

### 1. Go: Iteration Strategy Enum (`pkg/config/enums.go`)

**Delete:**
- `IterationStrategyReact = "react"`

**Add:**
- `IterationStrategyLangChain = "langchain"`

**Keep:**
- `IterationStrategyNativeThinking = "native-thinking"`
- `IterationStrategySynthesis = "synthesis"`
- `IterationStrategySynthesisNativeThinking = "synthesis-native-thinking"`

```go
const (
    IterationStrategyNativeThinking             IterationStrategy = "native-thinking"
    IterationStrategyLangChain                  IterationStrategy = "langchain"
    IterationStrategySynthesis                  IterationStrategy = "synthesis"
    IterationStrategySynthesisNativeThinking    IterationStrategy = "synthesis-native-thinking"
)
```

### 2. Go: Backend Resolution (`pkg/agent/config_resolver.go`)

`ResolveBackend()` keeps its original signature — strategy-only, no provider needed. The mapping is explicit and deterministic:

```go
func ResolveBackend(strategy config.IterationStrategy) string {
    switch strategy {
    case config.IterationStrategyNativeThinking,
         config.IterationStrategySynthesisNativeThinking:
        return BackendGoogleNative
    default:
        return BackendLangChain
    }
}
```

This is almost identical to the current implementation — we just swap `react` (which mapped to `langchain`) for the new `langchain` strategy (which also maps to `langchain`). No signature change, no auto-resolution:
- `native-thinking` → `google-native`
- `synthesis-native-thinking` → `google-native`
- `langchain` → `langchain`
- `synthesis` → `langchain`

### 3. Go: Controller Factory (`pkg/agent/controller/factory.go`)

Both `native-thinking` and `langchain` use the same controller — the logic is identical (structured tool calls via `ToolCallChunk`, thinking via `ThinkingChunk`, completion = no tool calls). The only difference is which backend processes the request.

```go
func (f *Factory) CreateController(strategy config.IterationStrategy, execCtx *agent.ExecutionContext) (agent.Controller, error) {
    switch strategy {
    case "":
        return nil, fmt.Errorf("iteration strategy is required (must be one of: native-thinking, langchain, synthesis, synthesis-native-thinking)")
    case config.IterationStrategyNativeThinking, config.IterationStrategyLangChain:
        return NewFunctionCallingController(), nil
    case config.IterationStrategySynthesis, config.IterationStrategySynthesisNativeThinking:
        return NewSynthesisController(), nil
    default:
        return nil, fmt.Errorf("unknown iteration strategy: %q", strategy)
    }
}
```

### 4. Go: Controller Rename and Cleanup

**Rename** `NativeThinkingController` → `FunctionCallingController`:
- File: `native_thinking.go` → `function_calling.go`
- Struct: `NativeThinkingController` → `FunctionCallingController`
- Constructor: `NewNativeThinkingController()` → `NewFunctionCallingController()`
- The controller logic is already provider-agnostic:
  - Sends tools via `GenerateInput.Tools` (native function calling)
  - Processes `ToolCallChunk` for structured tool calls
  - `ThinkingChunk` and `GroundingChunk` handling is additive (graceful no-op for non-Gemini providers)
  - Completion signal: response without tool calls = final answer

**Why rename?** The controller is now shared by both `native-thinking` and `langchain` strategies. Calling it "NativeThinkingController" would be misleading when used with the `langchain` strategy. `FunctionCallingController` accurately describes what it does regardless of backend.

**Delete entirely:**
- `react.go` — The entire ReActController
- `react_parser.go` — All text-based parsing (ParseReActResponse, extractSections, etc.)
- `react_parser_test.go` — All parser tests
- `react_test.go` — All ReAct controller tests
- ReAct-aware streaming in `streaming.go`:
  - `callLLMWithReActStreaming()` — entire function (~300 lines)
  - `failOpenStreamingEvent()` — only used by ReAct streaming
  - `DetectReActPhase()`, `StreamingPhase` struct
  - `reactPhaseIdle/Thought/Action/FinalAnswer` constants
  - `StreamedResponse.ReactThoughtStreamed`, `FinalAnswerStreamed` fields
**Keep in `streaming.go`:**
- `collectStream()`, `collectStreamWithCallback()`, `callLLM()`, `callLLMWithStreaming()` — used by FunctionCallingController
- `StreamedResponse` (remove ReAct fields)
- `mergeMetadata()`, `markStreamingEventsFailed()`
- `LLMResponse` struct

### 5. Go: PromptBuilder Interface (`pkg/agent/context.go`)

**Delete from interface:**
- `BuildReActMessages(...)` — no longer needed

**Rename:**
- `BuildNativeThinkingMessages(...)` → `BuildFunctionCallingMessages(...)`

**Keep:**
- `BuildSynthesisMessages(...)`
- `BuildForcedConclusionPrompt(...)` — simplify (remove ReAct branch)
- All other methods (summarization, executive summary)

### 6. Go: Prompt Templates (`pkg/agent/prompt/templates.go`)

**Delete:**
- `reactFormatOpener`
- `chatReActFormatOpener`
- `reactFormatBody`
- `reactFormatInstructions`
- `chatReActFormatInstructions`
- `reactForcedConclusionFormat`

**Keep:**
- `nativeThinkingForcedConclusionFormat` (used by `BuildForcedConclusionPrompt` for both strategies)
- `forcedConclusionTemplate`
- All other templates (synthesis, summarization, executive summary)

### 7. Go: Prompt Builder (`pkg/agent/prompt/builder.go`)

**Delete:**
- `BuildReActMessages()` method entirely

**Rename:**
- `BuildNativeThinkingMessages()` → `BuildFunctionCallingMessages()`

**Simplify:**
- `BuildForcedConclusionPrompt()` — remove the `IterationStrategyReact` case. The single `nativeThinkingForcedConclusionFormat` works for all strategies (it just says "provide a clear, structured conclusion").
- `buildInvestigationUserMessage()` — remove tool-description text injection (tools are bound natively, not described in text)

### 8. Go: Prompt Tools (`pkg/agent/prompt/tools.go`)

**Delete entirely** — `FormatToolDescriptions()` and `extractParameters()` were only used for injecting tool descriptions into ReAct text prompts. With native function calling, tools are sent as structured `ToolDefinition` in the gRPC request, not as text in prompts.

### 9. Go: ReAct Parser (`pkg/agent/controller/react_parser.go`)

**Delete entirely:**
- `ParsedReActResponse` struct
- `ParseReActResponse()`, `extractSections()`, `isSectionHeader()`
- `shouldStopParsing()`, `hasMidlineAction()`, `hasMidlineFinalAnswer()`
- All regex patterns (`midlineActionPattern`, etc.)
- `FormatObservation()`, `FormatToolErrorObservation()`, `FormatUnknownToolError()`
- `FormatErrorObservation()`, `ExtractForcedConclusionAnswer()`
- `GetFormatErrorFeedback()`, `GetFormatCorrectionReminder()`
- `validateToolName()`, `recoverMissingAction()`

### 10. Go: Helpers (`pkg/agent/controller/helpers.go`)

**Delete:**
- `buildToolNameSet()` — only used by ReActController for text-based tool name validation

**Keep:**
- Everything else (accumulateUsage, recordLLMInteraction, isTimeoutError, generateCallID, failedResult, etc.)

### 11. Go: Builtin Config (`pkg/config/builtin.go`)

**Update:**
- `KubernetesAgent.Description`: Remove "using ReAct pattern" → "Kubernetes-specialized agent"
- `KubernetesAgent.IterationStrategy`: Remove entirely (inherit from `defaults.iteration_strategy`) (Q13)

The `deploy/config/tarsy.yaml` sets `native-thinking` as the system default. When a user switches to OpenAI/Anthropic, they change the default strategy to `langchain` — all agents inherit automatically.

### 12. Go: E2E Tests

**Delete:**
- `test/e2e/react_streaming_test.go` — entire file

**Update:**
- `test/e2e/pipeline_test.go` — change strategy from `react` to `langchain` (or `native-thinking`)
- `test/e2e/testdata/expected_events.go` — update event expectations (remove `source: "react"` metadata)

### 13. Go: All Test Files Using `IterationStrategyReact`

Every test file that references `config.IterationStrategyReact` needs updating to use either `config.IterationStrategyNativeThinking` or `config.IterationStrategyLangChain` depending on what the test exercises.

**Heuristic:** Tests that test the function-calling controller path (tool calls, thinking chunks, streaming) should use `IterationStrategyNativeThinking` or `IterationStrategyLangChain` — both map to the same controller. Tests that just need *any valid* strategy can use either.

Affected files:
- `pkg/queue/executor_integration_test.go` (~6 references)
- `pkg/services/session_service_test.go`
- `pkg/services/interaction_service_test.go` (~8 references)
- `pkg/services/timeline_service_test.go` (~7 references)
- `pkg/services/stage_service_test.go` (~10 references)
- `pkg/services/message_service_test.go` (~2 references)
- `pkg/queue/chat_executor_test.go`
- `pkg/queue/chat_executor_integration_test.go`
- `pkg/agent/config_resolver_test.go`
- `pkg/agent/controller/factory_test.go`
- `pkg/agent/controller/streaming_test.go`
- `pkg/agent/controller/native_thinking_test.go` (renamed to `function_calling_test.go`)
- `pkg/agent/controller/lifecycle_integration_test.go`
- `pkg/agent/controller/synthesis_test.go`
- `pkg/agent/prompt/builder_test.go`
- `pkg/agent/prompt/builder_integration_test.go`
- `pkg/agent/factory_test.go`
- `pkg/config/enums_test.go`
- `pkg/config/builtin_test.go`
- `pkg/config/merge_test.go`

Strategy: global find-and-replace `IterationStrategyReact` → `IterationStrategyLangChain`, then review tests that specifically tested ReAct behavior (those should be deleted or rewritten).

---

## Python: LangChain Provider

### 14. New File: `llm-service/llm/providers/langchain_provider.py`

This is the core new implementation. It replaces `langchain_stub.py`.

#### Provider Architecture

```python
class LangChainProvider(LLMProvider):
    """Multi-provider LLM backend using LangChain.

    Supports: OpenAI, Anthropic, xAI, Google (via LangChain), VertexAI.
    Features: streaming, native thinking/reasoning, function calling.
    """

    async def generate(self, request: pb.GenerateRequest) -> AsyncIterator[pb.GenerateResponse]:
        # 1. Create/cache LangChain chat model for this provider
        # 2. Convert proto messages → LangChain messages
        # 3. Bind tools if provided
        # 4. Stream response with astream()
        # 5. Convert LangChain chunks → proto GenerateResponse
```

#### Model Instantiation (per provider type)

```python
def _create_chat_model(self, config: pb.LLMConfig) -> BaseChatModel:
    match config.provider:
        case "openai":
            return ChatOpenAI(
                model=config.model,
                api_key=os.getenv(config.api_key_env),
                streaming=True,
            )
        case "anthropic":
            return ChatAnthropic(
                model=config.model,
                api_key=os.getenv(config.api_key_env),
                streaming=True,
                max_tokens=32000,  # Anthropic requires explicit max output tokens
            )
        case "xai":
            return ChatXAI(
                model=config.model,
                api_key=os.getenv(config.api_key_env),
                streaming=True,
            )
        case "google":
            return ChatGoogleGenerativeAI(
                model=config.model,
                google_api_key=os.getenv(config.api_key_env),
                streaming=True,
            )
        case "vertexai":
            if "claude" in config.model or "anthropic" in config.model:
                return ChatAnthropicVertex(
                    model=config.model,
                    project=config.project,
                    location=config.location,
                    max_tokens=32000,
                )
            else:
                return ChatGoogleGenerativeAI(
                    model=config.model,
                    project=config.project,
                    location=config.location,
                    streaming=True,
                )
```

**Note on VertexAI:** Auto-detect model family from model name (Q3). Claude models → `ChatAnthropicVertex` (from `langchain_google_vertexai.model_garden`), Gemini models → `ChatGoogleGenerativeAI` with `project`/`location`.

#### Message Conversion

```python
def _convert_messages(self, messages: list[pb.ConversationMessage]) -> list[BaseMessage]:
    """Convert proto messages to LangChain message objects."""
    result = []
    for msg in messages:
        match msg.role:
            case "system":
                result.append(SystemMessage(content=msg.content))
            case "user":
                result.append(HumanMessage(content=msg.content))
            case "assistant":
                content = msg.content or ""
                tool_calls = []
                for tc in msg.tool_calls:
                    tool_calls.append({
                        "id": tc.id,
                        "name": tool_name_to_api(tc.name),
                        "args": json.loads(tc.arguments) if tc.arguments else {},
                    })
                result.append(AIMessage(content=content, tool_calls=tool_calls))
            case "tool":
                result.append(ToolMessage(
                    content=msg.content,
                    tool_call_id=msg.tool_call_id,
                    name=tool_name_to_api(msg.tool_name),
                ))
    return result
```

#### Tool Binding

```python
def _bind_tools(self, model: BaseChatModel, tools: list[pb.ToolDefinition]) -> BaseChatModel:
    """Bind MCP tools to the model via LangChain's bind_tools()."""
    langchain_tools = []

    for tool in tools:
        schema = json.loads(tool.parameters_schema) if tool.parameters_schema else {}
        langchain_tools.append({
            "type": "function",
            "function": {
                "name": tool_name_to_api(tool.name),
                "description": tool.description,
                "parameters": schema,
            }
        })

    if langchain_tools:
        return model.bind_tools(langchain_tools)
    return model
```

#### Tool Name Encoding

Shared utility (see Q4 in Questions doc):

```python
# llm/providers/tool_names.py
def tool_name_to_api(name: str) -> str:
    """Convert canonical 'server.tool' to 'server__tool' for LLM APIs."""
    return name.replace(".", "__")

def tool_name_from_api(name: str) -> str:
    """Convert 'server__tool' back to canonical 'server.tool'."""
    return name.replace("__", ".")
```

#### Streaming Response Conversion

The key streaming logic processes LangChain's `AIMessageChunk` objects:

```python
async def _stream_response(self, model, messages) -> AsyncIterator[pb.GenerateResponse]:
    """Stream LangChain response, converting to proto chunks."""
    async for chunk in model.astream(messages):
        # Process unified content_blocks (LangChain v1 API)
        for block in chunk.content_blocks:
            block_type = block.get("type")

            if block_type == "reasoning":
                # Thinking/reasoning — unified across all providers
                if reasoning := block.get("reasoning"):
                    yield pb.GenerateResponse(
                        thinking=pb.ThinkingDelta(content=reasoning)
                    )

            elif block_type == "tool_call_chunk":
                # Accumulate tool call chunks (LangChain may split them)
                # Yield complete ToolCallDelta when fully assembled
                ...

            elif block_type == "text":
                if text := block.get("text"):
                    yield pb.GenerateResponse(
                        text=pb.TextDelta(content=text)
                    )

        # Usage metadata (typically on last chunk)
        if chunk.usage_metadata:
            yield pb.GenerateResponse(
                usage=pb.UsageInfo(
                    input_tokens=chunk.usage_metadata.get('input_tokens', 0),
                    output_tokens=chunk.usage_metadata.get('output_tokens', 0),
                    total_tokens=chunk.usage_metadata.get('total_tokens', 0),
                )
            )

    yield pb.GenerateResponse(is_final=True)
```

#### Model Caching

Cache `BaseChatModel` instances per `(provider_type, model, api_key_env)` tuple (Q10). LangChain model objects are stateless — conversation state is passed per-call via messages. This avoids re-reading env vars and re-initializing HTTP clients on every request.

```python
def _get_or_create_model(self, config: pb.LLMConfig, tools: list[pb.ToolDefinition]) -> BaseChatModel:
    cache_key = (config.provider, config.model, config.api_key_env)
    if cache_key not in self._model_cache:
        self._model_cache[cache_key] = self._create_chat_model(config)
    model = self._model_cache[cache_key]
    if tools:
        model = self._bind_tools(model, tools)
    return model
```

#### Retry Logic

Same pattern as `GoogleNativeProvider`:
- MAX_RETRIES = 3
- Exponential backoff
- Only retry when 0 chunks have been yielded (safe retry)
- Classify retryable errors (rate limits, 5xx, timeouts)

#### Thinking/Reasoning Extraction

LangChain v1 standardizes thinking via `content_blocks` on `AIMessage` and `AIMessageChunk`. All providers use the same unified type:

| Provider | Block Type | Content Key |
|----------|-----------|-------------|
| Google Gemini | `"reasoning"` | `block["reasoning"]` |
| Anthropic Claude | `"reasoning"` | `block["reasoning"]` |
| OpenAI o-series | `"reasoning"` | `block["reasoning"]` |
| xAI Grok | `"reasoning"` | `block["reasoning"]` |

The type is **always `"reasoning"`** across all providers — LangChain normalizes provider-specific formats into this unified API. The provider extracts these and yields `ThinkingDelta` proto chunks, so the Go-side `FunctionCallingController` handles them uniformly.

#### Grounding Support

For non-Gemini providers, grounding is not applicable and no `GroundingDelta` is emitted. Gemini models use the `google-native` backend by default (via `native-thinking` strategy), which has full grounding support. If a user explicitly configures Gemini with the `langchain` strategy, they accept losing grounding metadata (Q6).

### 15. Delete: `llm-service/llm/providers/langchain_stub.py`

The stub is replaced by the real `LangChainProvider`.

### 16. Update: `llm-service/llm/servicer.py`

```python
class LLMServicer(pb_grpc.LLMServiceServicer):
    def __init__(self):
        self._registry = ProviderRegistry()
        google = GoogleNativeProvider()
        self._registry.register("google-native", google)
        self._registry.register("langchain", LangChainProvider())
        logger.info("LLM Servicer initialized with providers: google-native, langchain")
```

### 17. Update: `llm-service/pyproject.toml`

Add LangChain dependencies:

```toml
dependencies = [
    "grpcio>=1.76.0",
    "grpcio-tools>=1.76.0",
    "google-genai>=1.62.0",
    "pydantic>=2.12.5",
    "python-dotenv>=1.2.1",
    # LangChain (versions verified Feb 2026)
    "langchain-core>=1.2.13",
    "langchain-openai>=1.1.9",
    "langchain-anthropic>=1.3.3",
    "langchain-xai>=1.2.2",
    "langchain-google-genai>=4.2.0",
    "langchain-google-vertexai>=3.2.2",  # For ChatAnthropicVertex (Claude on VertexAI)
]
```

---

## Detailed File-by-File Change List

### Files to DELETE

| File | Reason |
|------|--------|
| `pkg/agent/controller/react.go` | Entire ReActController |
| `pkg/agent/controller/react_parser.go` | Text-based ReAct parsing |
| `pkg/agent/controller/react_parser_test.go` | Parser tests |
| `pkg/agent/controller/react_test.go` | Controller tests |
| `pkg/agent/prompt/tools.go` | FormatToolDescriptions (ReAct text injection) |
| `pkg/agent/prompt/tools_test.go` | Tests for deleted tools.go |
| `test/e2e/react_streaming_test.go` | ReAct streaming e2e test |
| `llm-service/llm/providers/langchain_stub.py` | Replaced by real provider |

### Files to CREATE

| File | Purpose |
|------|---------|
| `llm-service/llm/providers/langchain_provider.py` | Real LangChain multi-provider implementation |
| `llm-service/llm/providers/tool_names.py` | Shared tool name encoding utility |

### Files to RENAME

| From | To | Reason |
|------|----|--------|
| `pkg/agent/controller/native_thinking.go` | `pkg/agent/controller/function_calling.go` | Controller now shared by `native-thinking` and `langchain` strategies |
| `pkg/agent/controller/native_thinking_test.go` | `pkg/agent/controller/function_calling_test.go` | Test file follows controller |

### Files to MODIFY (Go)

| File | Changes |
|------|---------|
| `pkg/config/enums.go` | Delete `IterationStrategyReact`; add `IterationStrategyLangChain` |
| `pkg/config/enums_test.go` | Update strategy validation tests |
| `pkg/agent/config_resolver.go` | Update `ResolveBackend` — swap `react` case for `langchain` (same signature) |
| `pkg/agent/config_resolver_test.go` | Update backend resolution tests |
| `pkg/agent/context.go` | Remove `BuildReActMessages` from `PromptBuilder` interface; rename `BuildNativeThinkingMessages` → `BuildFunctionCallingMessages` |
| `pkg/agent/controller/factory.go` | Both `native-thinking` and `langchain` → `NewFunctionCallingController()` |
| `pkg/agent/controller/factory_test.go` | Update factory tests |
| `pkg/agent/controller/streaming.go` | Delete ReAct streaming functions; remove `ReactThoughtStreamed`/`FinalAnswerStreamed` from `StreamedResponse` |
| `pkg/agent/controller/streaming_test.go` | Remove ReAct streaming tests |
| `pkg/agent/controller/helpers.go` | Remove `buildToolNameSet()` |
| `pkg/agent/controller/helpers_test.go` | Remove tests for deleted helpers |
| `pkg/agent/controller/tool_execution.go` | Update comments (remove ReAct references) |
| `pkg/agent/prompt/builder.go` | Delete `BuildReActMessages()`; rename `BuildNativeThinkingMessages()` → `BuildFunctionCallingMessages()`; simplify `BuildForcedConclusionPrompt()` |
| `pkg/agent/prompt/builder_test.go` | Remove ReAct tests, update method names |
| `pkg/agent/prompt/builder_integration_test.go` | Remove ReAct tests, update method names |
| `pkg/agent/prompt/templates.go` | Delete all ReAct templates |
| `pkg/config/builtin.go` | Update KubernetesAgent (remove hardcoded strategy, update description) |
| `pkg/config/builtin_test.go` | Update strategy expectations |
| `pkg/config/merge_test.go` | Update strategy references |
| `pkg/config/validator_test.go` | Update strategy references |
| `pkg/agent/iteration.go` | Update any strategy references |
| `pkg/agent/factory_test.go` | Update strategy references |
| `pkg/events/types.go` | Update package comment — remove ReAct lifecycle pattern docs (Pattern 2 ReAct branch, "Note: the same event_type (llm_thinking) follows different patterns") |
| `pkg/events/manager.go` | Remove ReAct references if any |
| `pkg/events/integration_test.go` | Update strategy references |
| `pkg/services/session_service.go` | Update strategy references |
| `pkg/services/session_service_test.go` | Update strategy references |
| `pkg/services/interaction_service_test.go` | Update strategy references |
| `pkg/services/timeline_service_test.go` | Update strategy references |
| `pkg/services/stage_service_test.go` | Update strategy references |
| `pkg/services/message_service_test.go` | Update strategy references |
| `pkg/queue/executor_integration_test.go` | Update strategy references |
| `pkg/queue/chat_executor_test.go` | Update strategy references |
| `pkg/queue/chat_executor_integration_test.go` | Update strategy references |
| `pkg/agent/context/investigation_formatter.go` | Update ReAct references |
| `pkg/agent/context/investigation_formatter_test.go` | Update ReAct references |
| `pkg/mcp/router.go` | Update ReAct references if any |
| `pkg/api/server.go` | Update ReAct references if any |
| `ent/schema/agentexecution.go` | Update comment — remove `'react'` from strategy examples (currently says "e.g., 'react', 'native_thinking'") |
| `ent/schema/timelineevent.go` | Update `llm_thinking` comment — remove ReAct reference ("metadata.source = react"). After cleanup, thinking is always `source: "native"` |
| `test/e2e/pipeline_test.go` | Update strategy references |
| `test/e2e/testdata/expected_events.go` | Update event expectations |

### Files to MODIFY (Python)

| File | Changes |
|------|---------|
| `llm-service/llm/servicer.py` | Register `LangChainProvider` instead of stub |
| `llm-service/llm/providers/google_native.py` | Extract tool name helpers to shared utility |
| `llm-service/pyproject.toml` | Add LangChain dependencies |

### Files to MODIFY (Dashboard)

| File | Changes |
|------|---------|
| `web/dashboard/src/utils/timelineParser.ts` | Delete `isReActResponse()` function — no longer needed |
| `web/dashboard/src/components/timeline/TimelineItem.tsx` | Remove `isReActResponse()` import and usage |
| `web/dashboard/src/constants/eventTypes.ts` | Remove `NATIVE_THINKING: 'native_thinking'` — the Go backend only emits `llm_thinking` for all thinking events; `native_thinking` was never a real event type |
| `web/dashboard/src/utils/timelineParser.ts` | Remove `NATIVE_THINKING` mapping from `EVENT_TYPE_TO_FLOW_ITEM` |
| `web/dashboard/src/components/streaming/StreamingContentRenderer.tsx` | Remove `NATIVE_THINKING` branch — only check `LLM_THINKING` |
| `web/dashboard/src/components/shared/EmojiIcon.tsx` | Simplify `tooltipType` — remove `'native_thinking'` option (all thinking is just `'thought'` now) |
| `web/dashboard/src/components/shared/ContentPreviewTooltip.tsx` | Simplify `type` union — remove `'native_thinking'` |

**Note:** The dashboard is almost entirely strategy-agnostic — it renders based on event types (`llm_thinking`, `llm_response`, `llm_tool_call`, etc.), not iteration strategy. Both `native-thinking` and `langchain` produce identical event streams. The cleanup is:
1. **`isReActResponse()`** — dead code, ReAct text markers no longer exist
2. **`NATIVE_THINKING` event type** — the Go schema only has `llm_thinking`; `native_thinking` was an unused dashboard-side constant
3. **`'thought'` vs `'native_thinking'` tooltip types** — with ReAct gone, all thinking is the same; simplify to just `'thought'`

### Files to MODIFY (Config)

| File | Changes |
|------|---------|
| `deploy/config/tarsy.yaml` | Verify `iteration_strategy: "native-thinking"` still valid (it is — unchanged) |
| `deploy/config/llm-providers.yaml` | Verify provider configs work with new backend resolution |
| `docs/architecture-context.md` | Update to reflect new strategy set and backend resolution |

---

## Implementation Plan

### Step 1: Go — ReAct Deletion and Strategy Cleanup (~large)

1. Update `pkg/config/enums.go` — delete `react`, add `langchain`
2. Update `pkg/agent/config_resolver.go` — swap `react` for `langchain` in `ResolveBackend` (same signature)
3. Rename `NativeThinkingController` → `FunctionCallingController` (file + struct + constructor)
4. Update `pkg/agent/controller/factory.go` — both strategies map to same controller
5. Delete `react.go`, `react_parser.go`, `react_parser_test.go`, `react_test.go`
6. Clean up `streaming.go` — remove all ReAct streaming code
7. Update `pkg/agent/context.go` — remove `BuildReActMessages`, rename `BuildNativeThinkingMessages`
8. Update `pkg/agent/prompt/builder.go` — delete ReAct methods, rename, simplify
9. Delete `pkg/agent/prompt/tools.go`
10. Update `pkg/agent/prompt/templates.go` — delete ReAct templates
11. Update `pkg/config/builtin.go` — KubernetesAgent cleanup
12. Delete `test/e2e/react_streaming_test.go`
13. Global find-and-replace: `IterationStrategyReact` → `IterationStrategyLangChain` (note: `IterationStrategySynthesisNativeThinking` is kept unchanged)
14. Run `go build ./...` and `go test ./...` to verify

### Step 2: Python — LangChain Provider (~medium)

1. Create `llm-service/llm/providers/tool_names.py` — shared utility
2. Update `google_native.py` to use shared utility
3. Create `llm-service/llm/providers/langchain_provider.py`
4. Implement model instantiation for all 5 provider types
5. Implement message conversion (proto → LangChain)
6. Implement tool binding with `bind_tools()`
7. Implement streaming response conversion (LangChain → proto)
8. Implement thinking/reasoning extraction from `content_blocks`
9. Implement retry logic and error classification
10. Delete `langchain_stub.py`
11. Update `servicer.py` to register real provider
12. Update `pyproject.toml` with dependencies
13. Write unit tests

### Step 3: Integration Testing (~medium)

1. Test each provider type with real API keys:
   - OpenAI (o3) — function calling + reasoning blocks
   - Anthropic (Claude) — function calling + extended thinking
   - xAI (Grok) — function calling + reasoning
   - VertexAI — function calling
2. Test Google via `google-native` backend still works (regression)
3. E2E pipeline test with non-Gemini provider
4. Test tool name encoding round-trip (`server.tool` ↔ `server__tool`)
5. Test streaming correctness (chunks arrive in order, events created properly)

### Step 4: Dashboard Cleanup (~small)

1. Delete `isReActResponse()` from `timelineParser.ts` and its usage in `TimelineItem.tsx`
2. Remove `NATIVE_THINKING` constant from `eventTypes.ts` and all references (timelineParser, StreamingContentRenderer)
3. Simplify tooltip types — remove `'native_thinking'` from `EmojiIcon.tsx` and `ContentPreviewTooltip.tsx`
4. Verify dashboard renders correctly for both `native-thinking` and `langchain` strategies

### Step 5: Config and Documentation (~small)

1. Verify `deploy/config/tarsy.yaml` — `native-thinking` still works
2. Update `docs/architecture-context.md` — strategy/backend docs
3. Update `docs/project-plan.md` — mark Phase 8.2 complete

---

## Proto Changes

**No proto changes required** for the core feature. The existing `GenerateRequest`/`GenerateResponse` protocol already supports:
- Structured tool calls (`ToolCallDelta`)
- Thinking content (`ThinkingDelta`)
- Grounding metadata (`GroundingDelta`)
- Code execution (`CodeExecutionDelta`)
- Usage info (`UsageInfo`)
- Error info (`ErrorInfo`)
- Backend routing (`LLMConfig.backend`)
- Provider type (`LLMConfig.provider`)

**No optional proto changes needed.** Anthropic's `max_tokens` is hardcoded in the Python provider (Q8=A).

---

## Migration / Backward Compatibility

### Config Migration

Hard break (Q9). The `react` strategy value is simply removed — config validation rejects it with a clear error listing valid strategies (`native-thinking`, `langchain`, `synthesis`, `synthesis-native-thinking`). No auto-migration code.

### Database

No schema changes needed. The `iteration_strategy` field in `agent_executions` stores a string; new executions will write `"langchain"` or `"native-thinking"`. Historical records with `"react"` remain valid — they're just historical data.

---

## Risk Assessment

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| LangChain streaming inconsistencies across providers | Medium | Medium | Test each provider independently; implement provider-specific workarounds if needed |
| `content_blocks` not available for all providers | Low | Low | LangChain v1 unified API; verified for Anthropic, OpenAI, Google, Ollama (Q5) |
| Large number of test file updates | Certain | Low | Mostly mechanical find-and-replace; CI catches any misses |
| VertexAI model detection edge cases | Low | Medium | Auto-detect from model name (Q3); covers `claude-*` and `gemini-*` patterns |
| Naming confusion between `langchain` strategy and `langchain` backend | Low | Low | They align — the strategy name explicitly says "use LangChain" |
