"""LangChain multi-provider LLM backend.

Supports: OpenAI, Anthropic, xAI, Google (via LangChain), VertexAI.
Features: streaming, native thinking/reasoning, function calling, retries.

Replaces langchain_stub.py with a real multi-provider implementation.
"""
import asyncio
import enum
import json
import logging
import os
import uuid
from typing import AsyncIterator, Dict, List, Optional, Tuple

from langchain_core.messages import (
    AIMessage,
    AIMessageChunk,
    BaseMessage,
    HumanMessage,
    SystemMessage,
    ToolMessage,
)

from llm_proto import llm_service_pb2 as pb
from llm.providers.base import LLMProvider
from llm.providers.tool_names import tool_name_to_api, tool_name_from_api

logger = logging.getLogger(__name__)

# Retry configuration (matches GoogleNativeProvider pattern)
MAX_RETRIES = 3
RETRY_BACKOFF_BASE = 2  # seconds


class ProviderType(str, enum.Enum):
    """Supported LangChain provider types.

    Values match the proto LLMConfig.provider field and Go-side
    config.LLMProviderType constants.
    """
    OPENAI = "openai"
    ANTHROPIC = "anthropic"
    XAI = "xai"
    GOOGLE = "google"
    VERTEXAI = "vertexai"


class LangChainProvider(LLMProvider):
    """Multi-provider LLM backend using LangChain.

    Supports: OpenAI, Anthropic, xAI, Google (via LangChain), VertexAI.
    Features:
    - Cached chat model instances per (provider, model, api_key_env)
    - Streaming via astream() with content_blocks for unified reasoning/text/tool_calls
    - Tool binding via bind_tools()
    - Tool name encoding via shared tool_names utility
    - Retry logic with exponential backoff (safe: only when 0 chunks yielded)
    - UsageInfo streaming
    """

    def __init__(self):
        # Cache BaseChatModel instances per (provider, model, api_key_env) tuple.
        # LangChain model objects are stateless — conversation state is passed
        # per-call via messages. This avoids re-reading env vars and
        # re-initializing HTTP clients on every request.
        self._model_cache: Dict[Tuple[str, str, str], object] = {}

    def _get_or_create_model(self, config: pb.LLMConfig, tools: List[pb.ToolDefinition]):
        """Get or create a cached LangChain chat model, with tools bound if provided."""
        cache_key = (config.provider, config.model, config.api_key_env)
        if cache_key not in self._model_cache:
            self._model_cache[cache_key] = self._create_chat_model(config)
        model = self._model_cache[cache_key]
        if tools:
            model = self._bind_tools(model, list(tools))
        return model

    # ── Reasoning/thinking configuration per provider ──────────────────

    @staticmethod
    def _get_google_thinking_kwargs(model: str) -> dict:
        """Return kwargs to enable thinking/reasoning for Google models.

        Mirrors the thinking configuration from GoogleNativeProvider._get_thinking_config
        but using the LangChain ChatGoogleGenerativeAI parameter names.
        """
        model_lower = model.lower()
        if "gemini-2.5-pro" in model_lower:
            return {"include_thoughts": True, "thinking_budget": 32768}
        elif "gemini-2.5-flash" in model_lower:
            return {"include_thoughts": True, "thinking_budget": 24576}
        else:
            return {"include_thoughts": True, "thinking_level": "high"}

    @staticmethod
    def _get_openai_reasoning_kwargs(model: str) -> dict:
        """Return kwargs to enable reasoning for OpenAI models.

        Uses the Responses API which properly streams reasoning summaries.
        Reasoning is enabled by default; only non-reasoning GPT-5 variants
        (chat, main) are excluded.
        """
        model_lower = model.lower()
        if model_lower.startswith("gpt-5") and any(
            tag in model_lower for tag in ("-chat", "-main")
        ):
            return {}
        return {
            "use_responses_api": True,
            "reasoning": {"effort": "high", "summary": "auto"},
        }

    @staticmethod
    def _get_anthropic_thinking_kwargs(_model: str) -> dict:
        """Return kwargs to enable extended thinking for Claude models.

        Extended thinking is enabled by default. budget_tokens must be
        less than max_tokens.  _model is accepted (but currently unused)
        to match the signature of the other _get_*_kwargs helpers.
        """
        return {
            "thinking": {"type": "enabled", "budget_tokens": 16000},
            "max_tokens": 32000,
        }

    @staticmethod
    def _get_xai_reasoning_kwargs(model: str) -> dict:
        """Return kwargs to enable reasoning for xAI Grok models.

        Reasoning is enabled by default; only explicitly non-reasoning
        or non-text models (code, image generation) are excluded.
        """
        model_lower = model.lower()
        if "non-reasoning" in model_lower:
            return {}
        if any(tag in model_lower for tag in ("code", "imagine")):
            return {}
        return {"reasoning_effort": "high"}

    def _create_chat_model(self, config: pb.LLMConfig):
        """Create a LangChain BaseChatModel for the given provider config."""
        try:
            provider = ProviderType(config.provider)
        except ValueError as err:
            supported = ", ".join(p.value for p in ProviderType)
            raise ValueError(
                f"Unsupported provider '{config.provider}'. "
                f"Supported: {supported}."
            ) from err

        api_key = os.getenv(config.api_key_env) if config.api_key_env else None

        def _require_api_key() -> str:
            """Validate that the API key env var is set and return its value."""
            if not api_key:
                raise ValueError(
                    f"Environment variable '{config.api_key_env}' is not set "
                    f"(required for provider '{provider.value}')"
                )
            return api_key

        if provider is ProviderType.OPENAI:
            from langchain_openai import ChatOpenAI
            reasoning_kwargs = self._get_openai_reasoning_kwargs(config.model)
            return ChatOpenAI(
                model=config.model,
                api_key=_require_api_key(),
                streaming=True,
                stream_usage=True,
                **reasoning_kwargs,
            )

        elif provider is ProviderType.ANTHROPIC:
            from langchain_anthropic import ChatAnthropic
            thinking_kwargs = self._get_anthropic_thinking_kwargs(config.model)
            base_kwargs = {
                "model": config.model,
                "api_key": _require_api_key(),
                "streaming": True,
                "max_tokens": 32000,
            }
            # thinking_kwargs may override max_tokens to ensure budget_tokens < max_tokens
            base_kwargs.update(thinking_kwargs)
            return ChatAnthropic(**base_kwargs)

        elif provider is ProviderType.XAI:
            from langchain_xai import ChatXAI
            reasoning_kwargs = self._get_xai_reasoning_kwargs(config.model)
            return ChatXAI(
                model=config.model,
                api_key=_require_api_key(),
                streaming=True,
                **reasoning_kwargs,
            )

        elif provider is ProviderType.GOOGLE:
            from langchain_google_genai import ChatGoogleGenerativeAI
            thinking_kwargs = self._get_google_thinking_kwargs(config.model)
            return ChatGoogleGenerativeAI(
                model=config.model,
                google_api_key=_require_api_key(),
                streaming=True,
                **thinking_kwargs,
            )

        elif provider is ProviderType.VERTEXAI:
            model_lower = config.model.lower()
            if "claude" in model_lower or "anthropic" in model_lower:
                from langchain_google_vertexai.model_garden import ChatAnthropicVertex
                thinking_kwargs = self._get_anthropic_thinking_kwargs(config.model)
                base_kwargs = {
                    "model": config.model,
                    "project": config.project,
                    "location": config.location,
                    "max_tokens": 32000,
                }
                base_kwargs.update(thinking_kwargs)
                return ChatAnthropicVertex(**base_kwargs)
            else:
                from langchain_google_genai import ChatGoogleGenerativeAI
                thinking_kwargs = self._get_google_thinking_kwargs(config.model)
                return ChatGoogleGenerativeAI(
                    model=config.model,
                    project=config.project,
                    location=config.location,
                    streaming=True,
                    **thinking_kwargs,
                )

    def _convert_messages(self, messages: List[pb.ConversationMessage]) -> List[BaseMessage]:
        """Convert proto messages to LangChain message objects."""
        result: List[BaseMessage] = []
        for idx, msg in enumerate(messages):
            if msg.role == "system":
                result.append(SystemMessage(content=msg.content))
            elif msg.role == "user":
                result.append(HumanMessage(content=msg.content))
            elif msg.role == "assistant":
                content = msg.content or ""
                tool_calls = []
                for tc in msg.tool_calls:
                    try:
                        args = json.loads(tc.arguments) if tc.arguments else {}
                    except json.JSONDecodeError:
                        logger.warning(
                            "Failed to parse tool call arguments as JSON, using empty args: %s",
                            tc.arguments,
                        )
                        args = {}
                    tool_calls.append({
                        "id": tc.id,
                        "name": tool_name_to_api(tc.name),
                        "args": args,
                    })
                result.append(AIMessage(content=content, tool_calls=tool_calls))
            elif msg.role == "tool":
                result.append(ToolMessage(
                    content=msg.content,
                    tool_call_id=msg.tool_call_id,
                    name=tool_name_to_api(msg.tool_name) if msg.tool_name else "",
                ))
            else:
                raise ValueError(
                    f"Unrecognized message role {msg.role!r} at index {idx}. "
                    f"Expected one of: system, user, assistant, tool."
                )
        return result

    @staticmethod
    def _bind_tools(model, tools: List[pb.ToolDefinition]):
        """Bind MCP tools to the model via LangChain's bind_tools()."""
        langchain_tools = []
        for tool in tools:
            try:
                schema = json.loads(tool.parameters_schema) if tool.parameters_schema else {}
            except json.JSONDecodeError:
                schema = {}
            langchain_tools.append({
                "type": "function",
                "function": {
                    "name": tool_name_to_api(tool.name),
                    "description": tool.description,
                    "parameters": schema,
                },
            })
        if langchain_tools:
            return model.bind_tools(langchain_tools)
        return model

    async def generate(
        self,
        request: pb.GenerateRequest,
    ) -> AsyncIterator[pb.GenerateResponse]:
        """Stream LLM responses via LangChain."""
        request_id = str(uuid.uuid4())[:8]
        config = request.llm_config
        logger.info(
            "[%s] Generate: provider=%s model=%s session=%s execution=%s",
            request_id, config.provider, config.model,
            request.session_id, request.execution_id,
        )

        try:
            model = self._get_or_create_model(config, list(request.tools))
        except (ValueError, ImportError) as e:
            code = "credentials" if "not set" in str(e) else "invalid_request"
            yield pb.GenerateResponse(
                error=pb.ErrorInfo(message=str(e), code=code, retryable=False),
                is_final=True,
            )
            return

        try:
            messages = self._convert_messages(list(request.messages))
        except ValueError as e:
            yield pb.GenerateResponse(
                error=pb.ErrorInfo(message=str(e), code="invalid_request", retryable=False),
                is_final=True,
            )
            return

        # Retry loop — same pattern as GoogleNativeProvider.
        # Only retry when zero chunks have been yielded to the caller.
        last_error: Optional[Exception] = None
        for attempt in range(MAX_RETRIES):
            chunks_yielded = 0
            try:
                async for chunk in self._stream_response(model, messages, request_id):
                    yield chunk
                    chunks_yielded += 1
                return  # Success
            except _RetryableError as e:
                if chunks_yielded > 0:
                    logger.exception(
                        "[%s] Retryable error after %d chunks already yielded, "
                        "cannot retry safely",
                        request_id, chunks_yielded,
                    )
                    yield pb.GenerateResponse(
                        error=pb.ErrorInfo(
                            message=f"Stream failed after partial output ({chunks_yielded} chunks): {e}",
                            code="partial_stream_error",
                            retryable=False,
                        ),
                        is_final=True,
                    )
                    return
                last_error = e
                delay = RETRY_BACKOFF_BASE ** attempt
                logger.warning(
                    "[%s] Retryable error (attempt %d/%d), retrying in %ds: %s",
                    request_id, attempt + 1, MAX_RETRIES, delay, e,
                )
                await asyncio.sleep(delay)
            except Exception as e:
                logger.exception("[%s] Non-retryable error", request_id)
                yield pb.GenerateResponse(
                    error=pb.ErrorInfo(
                        message=f"Generation failed: {e}",
                        code="provider_error",
                        retryable=False,
                    ),
                    is_final=True,
                )
                return

        # All retries exhausted
        yield pb.GenerateResponse(
            error=pb.ErrorInfo(
                message=f"Generation failed after {MAX_RETRIES} retries: {last_error}",
                code="max_retries",
                retryable=False,
            ),
            is_final=True,
        )

    async def _stream_response(
        self,
        model,
        messages: List[BaseMessage],
        request_id: str,
        timeout_seconds: int = 180,
    ) -> AsyncIterator[pb.GenerateResponse]:
        """Stream LangChain response, converting to proto chunks.

        Processes content_blocks for unified reasoning/text handling,
        and tool_call_chunks for progressive tool call accumulation.
        """
        has_content = False
        accumulated_input_tokens = 0
        accumulated_output_tokens = 0
        accumulated_total_tokens = 0

        # Accumulate tool call chunks by index.
        # LangChain may split tool calls across multiple chunks.
        # Each tool call has: name (first chunk), id (first chunk), args (progressive).
        pending_tool_calls: Dict[int, Dict] = {}

        try:
            async with asyncio.timeout(timeout_seconds):
                async for chunk in model.astream(messages):
                    if not isinstance(chunk, AIMessageChunk):
                        continue

                    # ── Extract reasoning/thinking and text from chunk ──
                    # Different providers surface reasoning differently:
                    #   Google:    content_blocks type="reasoning"
                    #   Anthropic: content list type="thinking"
                    #   OpenAI:    content_blocks type="reasoning" (Responses API)
                    #              OR additional_kwargs["reasoning_summary_chunk"]
                    content_handled = False

                    if hasattr(chunk, "content_blocks") and chunk.content_blocks:
                        for block in chunk.content_blocks:
                            block_type = block.get("type") if isinstance(block, dict) else None

                            if block_type == "reasoning":
                                reasoning = block.get("reasoning", "")
                                if reasoning:
                                    has_content = True
                                    content_handled = True
                                    yield pb.GenerateResponse(
                                        thinking=pb.ThinkingDelta(content=reasoning)
                                    )

                            elif block_type == "non_standard":
                                # Anthropic thinking blocks are wrapped as non_standard
                                value = block.get("value", {})
                                if isinstance(value, dict) and value.get("type") == "thinking":
                                    thinking_text = value.get("thinking", "")
                                    if thinking_text:
                                        has_content = True
                                        content_handled = True
                                        yield pb.GenerateResponse(
                                            thinking=pb.ThinkingDelta(content=thinking_text)
                                        )

                            elif block_type == "text":
                                text = block.get("text", "")
                                if text:
                                    has_content = True
                                    content_handled = True
                                    yield pb.GenerateResponse(
                                        text=pb.TextDelta(content=text)
                                    )

                    # OpenAI Responses API streaming: reasoning arrives via additional_kwargs
                    if hasattr(chunk, "additional_kwargs") and chunk.additional_kwargs:
                        reasoning_chunk = chunk.additional_kwargs.get("reasoning_summary_chunk")
                        if reasoning_chunk:
                            has_content = True
                            content_handled = True
                            yield pb.GenerateResponse(
                                thinking=pb.ThinkingDelta(content=reasoning_chunk)
                            )

                    # Fallback: plain string content (some providers don't use content_blocks)
                    if not content_handled and isinstance(chunk.content, str) and chunk.content:
                        has_content = True
                        yield pb.GenerateResponse(
                            text=pb.TextDelta(content=chunk.content)
                        )

                    # Fallback: list content (Google/Gemini native format).
                    # When content_blocks is empty but chunk.content is a list of
                    # content-part dicts, extract thinking/text from the parts directly.
                    # This handles Gemini's native list format:
                    #   {'type': 'thinking', 'thinking': '...'} → ThinkingDelta
                    #   {'type': 'text', 'text': '...'}         → TextDelta
                    elif not content_handled and isinstance(chunk.content, list) and chunk.content:
                        for part in chunk.content:
                            if not isinstance(part, dict):
                                continue
                            part_type = part.get("type")
                            if part_type == "thinking":
                                thinking_text = part.get("thinking", "")
                                if thinking_text:
                                    has_content = True
                                    content_handled = True
                                    yield pb.GenerateResponse(
                                        thinking=pb.ThinkingDelta(content=thinking_text)
                                    )
                            elif part_type == "text":
                                text = part.get("text", "")
                                if text:
                                    has_content = True
                                    content_handled = True
                                    yield pb.GenerateResponse(
                                        text=pb.TextDelta(content=text)
                                    )

                    # Process tool call chunks (progressive accumulation)
                    if hasattr(chunk, "tool_call_chunks") and chunk.tool_call_chunks:
                        for tc_chunk in chunk.tool_call_chunks:
                            idx = tc_chunk.get("index", 0)
                            if idx not in pending_tool_calls:
                                pending_tool_calls[idx] = {
                                    "id": "",
                                    "name": "",
                                    "args": "",
                                }
                            entry = pending_tool_calls[idx]
                            if name := tc_chunk.get("name"):
                                entry["name"] = name
                            if call_id := tc_chunk.get("id"):
                                entry["id"] = call_id
                            if args := tc_chunk.get("args"):
                                entry["args"] += args

                    # Accumulate usage metadata across streaming chunks.
                    # Google models report input_tokens on the first chunk and
                    # output_tokens incrementally on subsequent chunks.
                    if hasattr(chunk, "usage_metadata") and chunk.usage_metadata:
                        um = chunk.usage_metadata
                        if isinstance(um, dict):
                            accumulated_input_tokens += um.get("input_tokens", 0)
                            accumulated_output_tokens += um.get("output_tokens", 0)
                            accumulated_total_tokens += um.get("total_tokens", 0)
                        else:
                            accumulated_input_tokens += getattr(um, "input_tokens", 0)
                            accumulated_output_tokens += getattr(um, "output_tokens", 0)
                            accumulated_total_tokens += getattr(um, "total_tokens", 0)

        except asyncio.TimeoutError as exc:
            raise _RetryableError(f"[{request_id}] Generation timed out after {timeout_seconds}s") from exc

        # Emit accumulated tool calls as complete ToolCallDelta messages
        for _idx, tc_data in sorted(pending_tool_calls.items()):
            has_content = True
            call_id = tc_data["id"] or str(uuid.uuid4())[:8]
            yield pb.GenerateResponse(
                tool_call=pb.ToolCallDelta(
                    call_id=call_id,
                    name=tool_name_from_api(tc_data["name"]),
                    arguments=tc_data["args"] or "{}",
                )
            )

        if not has_content:
            raise _RetryableError(f"[{request_id}] Empty response from LLM (no content generated)")

        # Yield accumulated usage info after confirming content was produced
        if accumulated_input_tokens or accumulated_output_tokens:
            yield pb.GenerateResponse(
                usage=pb.UsageInfo(
                    input_tokens=accumulated_input_tokens,
                    output_tokens=accumulated_output_tokens,
                    total_tokens=accumulated_total_tokens,
                )
            )

        # Final chunk
        yield pb.GenerateResponse(is_final=True)


class _RetryableError(Exception):
    """Internal exception for retryable errors."""
    pass
