# Phase 8.2: Multi-LLM Support — Questions (ALL RESOLVED)

All questions have been reviewed and resolved. This document serves as a decision log.

---

## Q1: Strategy Naming — RESOLVED

**Decision:** Four strategies:
- `native-thinking` — Gemini native SDK (kept as-is)
- `langchain` — LangChain-based multi-provider (new, replaces `react`)
- `synthesis` — Synthesis stage, uses `langchain` backend (kept)
- `synthesis-native-thinking` — Synthesis stage, uses `google-native` backend (kept)

Only `react` is deleted. `synthesis-native-thinking` is retained to give users explicit control over which backend handles the synthesis stage — even when using the same underlying Google LLM provider, a user might prefer one backend over the other (e.g., for Gemini-specific features only available in the native SDK).

---

## Q2: Backend Resolution — RESOLVED

**Decision:** Pure strategy-based resolution (same approach as the current implementation). No auto-resolution, no heuristics, no provider inspection.

```
native-thinking            → google-native
synthesis-native-thinking  → google-native
langchain                  → langchain
synthesis                  → langchain
```

This is the simplest and most explicit approach. The `ResolveBackend(strategy)` function keeps its original signature — no provider parameter needed. Users control which backend handles synthesis by choosing `synthesis` (langchain) or `synthesis-native-thinking` (google-native).

**Why not auto-resolve `synthesis` from provider type?** Flexibility. A user with a Google provider might want synthesis to go through LangChain (e.g., if LangChain adds a useful feature), or vice versa. Explicit strategy names give full control without hidden heuristics.

---

## Q3: VertexAI Implementation — RESOLVED

**Decision:** Auto-detect model family from model name. VertexAI is a hosting layer that can run multiple model families. The provider type stays `vertexai`, and `LangChainProvider._create_chat_model()` inspects the model name to choose the right LangChain class:

- `claude-*` / `anthropic-*` → `ChatAnthropicVertex(model, project, location)`
- Everything else (Gemini) → `ChatGoogleGenerativeAI(model, project, location)`

```python
case "vertexai":
    if "claude" in config.model or "anthropic" in config.model:
        return ChatAnthropicVertex(
            model=config.model,
            project=config.project,
            location=config.location,
        )
    else:
        return ChatGoogleGenerativeAI(
            model=config.model,
            project=config.project,
            location=config.location,
        )
```

Same pattern as old TARSy. The `LLMProviderConfig` already has `ProjectEnv`/`LocationEnv` for VertexAI auth.

---

## Q4: Tool Name Encoding — RESOLVED

**Decision:** Extract to shared utility module `llm/providers/tool_names.py`. Both `GoogleNativeProvider` and `LangChainProvider` need `server.tool` ↔ `server__tool` conversion — having it in one place reduces drift risk:

```python
def tool_name_to_api(name: str) -> str:
    """Convert 'server.tool' to 'server__tool'."""
    return name.replace(".", "__")

def tool_name_from_api(name: str) -> str:
    """Convert 'server__tool' to 'server.tool'."""
    return name.replace("__", ".")
```

---

## Q5: LangChain Thinking/Reasoning — RESOLVED

**Decision:** Use LangChain v1's unified `content_blocks` API. This is the official, documented approach for accessing reasoning across all providers.

**Verified against LangChain v1 docs (2025):**

LangChain v1 introduced `content_blocks` as a provider-agnostic property on both `AIMessage` and `AIMessageChunk` (streaming). The API is consistent:

```python
# Non-streaming
response = model.invoke("...")
for block in response.content_blocks:
    if block["type"] == "reasoning":
        print(block["reasoning"])        # thinking/reasoning content
    elif block["type"] == "text":
        print(block["text"])             # text content
    elif block["type"] == "tool_call":
        print(block["name"], block["args"])

# Streaming — same API on each chunk
for chunk in model.stream("..."):
    for block in chunk.content_blocks:
        if block["type"] == "reasoning" and (reasoning := block.get("reasoning")):
            yield ThinkingDelta(content=reasoning)
        elif block["type"] == "tool_call_chunk":
            ...  # accumulate tool calls
        elif block["type"] == "text":
            yield TextDelta(content=block["text"])
```

Key facts:
- **All providers use `type: "reasoning"`** — not `"thinking"`. This is unified across Anthropic, OpenAI, Google, and Ollama.
- **Content key is `block["reasoning"]`** — not `block["thinking"]`.
- **Works during streaming** — `chunk.content_blocks` is populated incrementally.
- **Supported providers:** Anthropic (extended thinking), OpenAI (o-series reasoning), Google (Gemini thinking), Ollama.

**Fallback strategy:** If a specific provider doesn't populate `content_blocks` during streaming for some edge case, check `additional_kwargs` as a secondary source. But per the docs, this should not be needed with current LangChain v1 provider packages.

Thinking extraction is a must — thinking tokens are tracked in our token usage and displayed on the dashboard.

---

## Q6: Google Native Tools via LangChain — RESOLVED

**Decision:** Skip for now. Google models that want native tools (`google_search`, `code_execution`, `url_context`) should use the `native-thinking` strategy, which routes to the `google-native` backend with full native tool support. Users who explicitly configure Gemini with the `langchain` strategy accept losing native tools as a trade-off.

---

## Q7: Grounding Metadata via LangChain — RESOLVED

**Decision:** N/A — coupled to Q6. Since we skip native tools in LangChain (Q6=B), grounding metadata is irrelevant for the LangChain provider. Grounding only happens with native tools (Google Search, URL Context), which are handled by the `google-native` backend.

---

## Q8: Anthropic `max_tokens` Requirement — RESOLVED

**Decision:** Hardcode `max_tokens=32000` in the Python LangChain provider for Anthropic (and VertexAI-Anthropic). No Go config or proto changes needed.

Anthropic's API requires `max_tokens` (maximum **output** tokens) on every request. Current model limits (Feb 2026): Opus 4.6 = 128K, Sonnet 4.5 = 64K, Haiku 4.5 = 64K. 32000 (half of Sonnet's max) is generous for agent responses. Can be made configurable later if needed.

```python
case "anthropic":
    return ChatAnthropic(
        model=config.model,
        api_key=os.getenv(config.api_key_env),
        max_tokens=32000,
        streaming=True,
    )
```

---

## Q9: Config Migration — RESOLVED

**Decision:** Hard break. Reject `react` as an invalid strategy value — config validation will fail with a clear error message listing valid strategies. No auto-migration code.

Only one value is affected (`react` → `langchain`), and there are no external users to worry about. A clear validation error is simpler than migration code we'd need to clean up later.

---

## Q10: LangChain Model Caching — RESOLVED

**Decision:** Cache per `(provider_type, model, api_key_env)` tuple. LangChain model objects are stateless (conversation state is passed per-call via messages), so caching avoids re-reading env vars and re-initializing HTTP clients on every call. No TTL needed — API key rotations require a service restart (same as `GoogleNativeProvider`).

---

## Q11: Streaming Tool Call Accumulation — RESOLVED

**Decision:** Accumulate tool call chunks in the provider, yield complete `ToolCallDelta` when the stream ends. We don't need to stream tool calls incrementally — only thinking/reasoning and text responses need true streaming. Maintain a buffer per tool call index; when the stream ends, yield all completed `ToolCallDelta` messages. This matches how `GoogleNativeProvider` works (Gemini SDK delivers tool calls whole).

---

## Q12: Error Classification Across Providers — RESOLVED

**Decision:** Build a shared `classify_error()` function that normalizes exceptions based on error message patterns (rate, 429, 5xx, auth, timeout). Default behavior for unknown errors: treat as **non-retryable** and propagate with the original error message — don't silently swallow or ignore.

Known error patterns:

| Provider | Rate Limit | Server Error | Auth Error |
|----------|-----------|--------------|------------|
| OpenAI | `RateLimitError` | `APIStatusError(5xx)` | `AuthenticationError` |
| Anthropic | `RateLimitError` | `APIStatusError(5xx)` | `AuthenticationError` |
| xAI | OpenAI-compatible (same exceptions) | Same | Same |
| Google (LangChain) | `ResourceExhausted` | `GoogleAPIError` | `PermissionDenied` |

Classification returns `(code, retryable)`:
- Rate limit → `("rate_limit", True)`
- Server error (5xx) → `("server_error", True)`
- Timeout → `("timeout", True)`
- Auth error → `("auth_error", False)`
- **Unknown → `("unknown", False)`** — surface to caller, don't retry

---

## Q13: KubernetesAgent Default Strategy — RESOLVED

**Decision:** Remove the hardcoded strategy — inherit from `defaults.iteration_strategy`. The `deploy/config/tarsy.yaml` sets `native-thinking` as the system default. When a user switches to OpenAI/Anthropic, they change the default strategy to `langchain` and all agents pick it up automatically.

---

## Summary of Recommendations

| Q# | Topic | Recommendation |
|----|-------|---------------|
| Q1 | Strategy naming | RESOLVED: `native-thinking`, `langchain`, `synthesis`, `synthesis-native-thinking` |
| Q2 | Backend resolution | RESOLVED: Pure strategy-based, explicit mapping |
| Q3 | VertexAI | RESOLVED: Auto-detect model family from name |
| Q4 | Tool name encoding | RESOLVED: Shared utility module |
| Q5 | Thinking extraction | RESOLVED: LangChain v1 `content_blocks` with `type: "reasoning"` |
| Q6 | Google native tools via LangChain | RESOLVED: Skip for now (use google-native) |
| Q7 | Grounding via LangChain | RESOLVED: N/A (coupled to Q6) |
| Q8 | Anthropic max_tokens | RESOLVED: Hardcode 32000 in Python provider |
| Q9 | Config migration | RESOLVED: Hard break, reject old values |
| Q10 | Model caching | RESOLVED: Cache per (provider, model, api_key_env) |
| Q11 | Streaming tool calls | RESOLVED: Accumulate chunks, yield complete calls |
| Q12 | Error classification | RESOLVED: Shared classify_error(), unknown = non-retryable |
| Q13 | KubernetesAgent strategy | RESOLVED: Remove hardcoded strategy, inherit from defaults |
