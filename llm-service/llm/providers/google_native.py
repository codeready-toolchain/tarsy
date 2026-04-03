"""Google Gemini native provider using the google-genai SDK."""
import asyncio
import json
import logging
import os
import time
import uuid
from typing import Any, AsyncIterator, Dict, List, Optional, Tuple

from google import genai
from google.genai import errors as genai_errors
from google.genai import types as genai_types

from llm_proto import llm_service_pb2 as pb
from llm.providers.base import LLMProvider
from llm.providers.tool_names import tool_name_to_api, tool_name_from_api

logger = logging.getLogger(__name__)

# Retry configuration
MAX_RETRIES = 3
RETRY_BACKOFF_BASE = 2  # seconds
EMPTY_RESPONSE_RETRY_DELAY = 3  # seconds

# Model Content cache TTL — entries expire after this period.
# Executions typically complete in minutes; 1 hour is generous headroom.
MODEL_CONTENT_CACHE_TTL = 3600  # 1 hour


class GoogleNativeProvider(LLMProvider):
    """LLM provider using Google's native genai SDK.

    Features:
    - Cached SDK client (initialized once per API key)
    - Per-model thinking configuration
    - Model Content caching per execution for thought signature preservation
    - Tool definition -> FunctionDeclaration conversion
    - Tool name encoding via shared tool_names utility
    - Native tools (google_search, code_execution, url_context)
    - UsageInfo streaming
    - Transient retry logic
    - Error classification
    """

    def __init__(self):
        self._clients: Dict[str, genai.Client] = {}
        # Cache original model Content objects per execution.
        # Each Generate call appends one turn (a list of Content objects
        # collected from streaming chunks, exactly like the official SDK's
        # Chat class does). On subsequent calls, these are replayed verbatim
        # instead of reconstructing from proto fields — preserving all
        # thought_signatures, thinking Parts, and any other fields.
        # Key: execution_id -> (list of turns, timestamp)
        # Each turn is a list of Content objects from streaming chunks.
        self._model_contents: Dict[str, Tuple[List[List[genai_types.Content]], float]] = {}

    def _get_client(self, api_key_env: str) -> genai.Client:
        """Get or create a cached genai client for the given API key env var."""
        if api_key_env in self._clients:
            return self._clients[api_key_env]

        api_key = os.getenv(api_key_env)
        if not api_key:
            raise ValueError(
                f"Environment variable '{api_key_env}' is not set"
            )

        client = genai.Client(api_key=api_key)
        self._clients[api_key_env] = client
        logger.info("Created genai client for %s", api_key_env)
        return client

    def _get_thinking_config(self, model: str) -> genai_types.ThinkingConfig:
        """Get thinking configuration based on model name."""
        model_lower = model.lower()
        if "gemini-2.5-pro" in model_lower:
            return genai_types.ThinkingConfig(
                thinking_budget=32768,
                include_thoughts=True,
            )
        elif "gemini-2.5-flash" in model_lower:
            return genai_types.ThinkingConfig(
                thinking_budget=24576,
                include_thoughts=True,
            )
        else:
            # Default for Gemini 3 models and others
            return genai_types.ThinkingConfig(
                thinking_level=genai_types.ThinkingLevel.HIGH,
                include_thoughts=True,
            )

    def _convert_messages(
        self, messages: List[pb.ConversationMessage], execution_id: str = ""
    ) -> Tuple[Optional[str], List[genai_types.Content]]:
        """Convert proto messages to genai Contents.

        Returns (system_instruction, contents).
        System messages are extracted as system_instruction.
        Tool results (role=tool) are converted to FunctionResponse parts.

        For assistant (model) messages, cached Content objects from previous
        Generate calls are used when available. This preserves the original
        Gemini response verbatim — including thought_signatures on all Parts,
        thinking Parts, and any other fields the API returned. This follows
        the same pattern as the official google-genai SDK's Chat class.
        Falls back to reconstruction from proto fields on cache miss.
        """
        system_instruction = None
        contents: List[genai_types.Content] = []
        cached_turns = self._get_cached_model_turns(execution_id) if execution_id else []
        model_turn_idx = 0

        for idx, msg in enumerate(messages):
            if msg.role == "system":
                if system_instruction is not None:
                    raise ValueError(
                        f"Multiple system messages provided (duplicate at index {idx}). "
                        f"Only one system message is allowed."
                    )
                system_instruction = msg.content
            elif msg.role == "user":
                contents.append(
                    genai_types.Content(
                        role="user",
                        parts=[genai_types.Part(text=msg.content)],
                    )
                )
            elif msg.role == "assistant":
                if model_turn_idx < len(cached_turns):
                    # Use cached Content objects — preserves all thought_signatures
                    # and thinking Parts exactly as Gemini returned them.
                    contents.extend(cached_turns[model_turn_idx])
                else:
                    # Cache miss fallback: reconstruct from proto fields.
                    # This path is hit on first call (no history) or if the
                    # Python service restarted mid-execution.
                    parts: List[genai_types.Part] = []
                    if msg.content:
                        parts.append(genai_types.Part(text=msg.content))
                    for tc in msg.tool_calls:
                        try:
                            args = json.loads(tc.arguments) if tc.arguments else {}
                        except json.JSONDecodeError:
                            logger.warning(
                                "Failed to parse tool call arguments as JSON, using empty args: %s",
                                tc.arguments,
                            )
                            args = {}
                        parts.append(
                            genai_types.Part(
                                function_call=genai_types.FunctionCall(
                                    name=tool_name_to_api(tc.name),
                                    args=args,
                                )
                            )
                        )
                    contents.append(
                        genai_types.Content(role="model", parts=parts)
                    )
                model_turn_idx += 1
            elif msg.role == "tool":
                # Tool result -> FunctionResponse
                try:
                    result_data = json.loads(msg.content) if msg.content else {}
                except json.JSONDecodeError:
                    result_data = {"text": msg.content}
                contents.append(
                    genai_types.Content(
                        role="user",
                        parts=[
                            genai_types.Part(
                                function_response=genai_types.FunctionResponse(
                                    name=tool_name_to_api(msg.tool_name),
                                    response=result_data,
                                )
                            )
                        ],
                    )
                )
            else:
                raise ValueError(
                    f"Unrecognized message role {msg.role!r} at index {idx}. "
                    f"Expected one of: system, user, assistant, tool."
                )

        return system_instruction, contents

    @staticmethod
    def _is_image_model(model: str) -> bool:
        """Check if the model is an image-generation variant (limited native tool support)."""
        return "image" in model.lower()

    def _convert_tools(
        self, tools: List[pb.ToolDefinition], native_tools: Dict[str, bool],
        model: str = "",
    ) -> Optional[List[genai_types.Tool]]:
        """Convert proto tool definitions to genai Tool objects.

        Gemini 3+ models support combining function declarations with native
        grounding tools (url_context, google_search) in the same request.
        Both are included when present.

        Image model variants only support google_search; url_context and
        code_execution are filtered out to avoid 400 errors from the
        Gemini API.  Any skipped tools are logged at INFO level.
        """
        result_tools: List[genai_types.Tool] = []

        # Add function declarations (MCP / memory / built-in tools)
        if tools:
            declarations = []
            for tool in tools:
                try:
                    params = json.loads(tool.parameters_schema) if tool.parameters_schema else {}
                except json.JSONDecodeError:
                    params = {}
                declarations.append(
                    genai_types.FunctionDeclaration(
                        name=tool_name_to_api(tool.name),
                        description=tool.description,
                        parameters_json_schema=params if params else None,
                    )
                )
            result_tools.append(genai_types.Tool(function_declarations=declarations))

        # Add native grounding tools alongside any function declarations.
        # Image model variants only support google_search; url_context and
        # code_execution are not available and would cause a 400 error.
        if native_tools:
            is_image = self._is_image_model(model)
            if native_tools.get("google_search"):
                result_tools.append(genai_types.Tool(google_search=genai_types.GoogleSearch()))
            if native_tools.get("code_execution") and not is_image:
                result_tools.append(genai_types.Tool(code_execution=genai_types.ToolCodeExecution()))
            if native_tools.get("url_context") and not is_image:
                result_tools.append(genai_types.Tool(url_context=genai_types.UrlContext()))
            if is_image:
                skipped = [t for t in ("url_context", "code_execution") if native_tools.get(t)]
                if skipped:
                    logger.info("Skipping %s for image model %s (not supported)", ", ".join(skipped), model)
                if native_tools.get("url_context"):
                    logger.warning(
                        "Config requests url_context but model %r is an image variant — "
                        "UrlContext is omitted (API unsupported). Use a text Gemini provider "
                        "(e.g. google-default, gemini-3-flash) for WebResearcher / URL fetching.",
                        model,
                    )

        return result_tools if result_tools else None

    def _get_cached_model_turns(self, execution_id: str) -> List[List[genai_types.Content]]:
        """Retrieve cached model Content turns for an execution."""
        entry = self._model_contents.get(execution_id)
        if entry is None:
            return []
        turns, ts = entry
        if time.time() - ts > MODEL_CONTENT_CACHE_TTL:
            del self._model_contents[execution_id]
            return []
        return turns

    def _cache_model_turn(self, execution_id: str, turn_contents: List[genai_types.Content]) -> None:
        """Append a model turn's Content objects to the execution cache."""
        entry = self._model_contents.get(execution_id)
        if entry is None:
            turns: List[List[genai_types.Content]] = []
        else:
            turns = entry[0]
        turns.append(turn_contents)
        self._model_contents[execution_id] = (turns, time.time())
        # Evict expired executions
        now = time.time()
        expired = [k for k, (_, ts) in self._model_contents.items() if now - ts > MODEL_CONTENT_CACHE_TTL]
        for k in expired:
            del self._model_contents[k]

    async def generate(
        self,
        request: pb.GenerateRequest,
    ) -> AsyncIterator[pb.GenerateResponse]:
        """Stream LLM responses from Google Gemini."""
        request_id = str(uuid.uuid4())[:8]
        config = request.llm_config
        logger.info(
            "[%s] Generate: model=%s session=%s execution=%s",
            request_id, config.model, request.session_id, request.execution_id,
        )

        try:
            client = self._get_client(config.api_key_env)
        except ValueError as e:
            yield pb.GenerateResponse(
                error=pb.ErrorInfo(message=str(e), code="credentials", retryable=False),
                is_final=True,
            )
            return

        if request.clear_cache and request.execution_id:
            if request.execution_id in self._model_contents:
                del self._model_contents[request.execution_id]
                logger.info(
                    "[%s] Cleared model content cache for execution %s (provider fallback)",
                    request_id, request.execution_id,
                )

        try:
            system_instruction, contents = self._convert_messages(
                list(request.messages), request.execution_id
            )
            native_tools = dict(config.native_tools) if config.native_tools else {}
            tools = self._convert_tools(list(request.tools), native_tools, model=config.model)
        except ValueError as e:
            yield pb.GenerateResponse(
                error=pb.ErrorInfo(message=str(e), code="invalid_request", retryable=False),
                is_final=True,
            )
            return

        # Build generation config
        thinking_config = self._get_thinking_config(config.model)
        gen_config = genai_types.GenerateContentConfig(
            thinking_config=thinking_config,
            system_instruction=system_instruction,
        )
        if tools:
            gen_config.tools = tools
            has_func_decls = any(t.function_declarations for t in tools)
            has_native = any(
                t.google_search or t.url_context or t.code_execution
                for t in tools
            )
            if has_func_decls and has_native:
                gen_config.tool_config = genai_types.ToolConfig(
                    include_server_side_tool_invocations=True,
                )

        # Retry loop.
        # Only retry when zero chunks have been yielded to the caller.
        # Once any chunk is yielded over gRPC it cannot be retracted,
        # so retrying after partial output would corrupt the stream.
        last_error = None
        for attempt in range(MAX_RETRIES):
            chunks_yielded = 0
            try:
                async for chunk in self._stream_with_timeout(
                    client, config.model, contents, gen_config, request_id,
                    execution_id=request.execution_id,
                ):
                    yield chunk
                    chunks_yielded += 1
                return  # Success
            except _RetryableError as e:
                if chunks_yielded > 0:
                    # Partial output already sent — retrying would duplicate data
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
                content_summary = []
                for c in contents:
                    parts_info = []
                    for p in (c.parts or []):
                        if hasattr(p, "thought") and p.thought:
                            parts_info.append("thought")
                        elif hasattr(p, "function_call") and p.function_call:
                            parts_info.append(f"fc:{p.function_call.name}")
                        elif hasattr(p, "function_response") and p.function_response:
                            parts_info.append(f"fr:{p.function_response.name}")
                        elif hasattr(p, "text") and p.text:
                            parts_info.append(f"text:{len(p.text)}ch")
                        else:
                            parts_info.append("other")
                    content_summary.append(f"{c.role}[{','.join(parts_info)}]")
                logger.exception(
                    "[%s] Non-retryable error: model=%s, messages=%d, "
                    "contents=%d, tools=%d, content_structure=%s",
                    request_id, config.model, len(list(request.messages)),
                    len(contents), len(list(request.tools)),
                    " | ".join(content_summary),
                )
                yield pb.GenerateResponse(
                    error=pb.ErrorInfo(
                        message=f"Generation failed: {e}",
                        code="provider_error",
                        retryable=False,
                    ),
                    is_final=True,
                )
                return

        # All retries exhausted (only reached when zero chunks were yielded each time)
        yield pb.GenerateResponse(
            error=pb.ErrorInfo(
                message=f"Generation failed after {MAX_RETRIES} retries: {last_error}",
                code="max_retries",
                retryable=False,
            ),
            is_final=True,
        )

    async def _stream_with_timeout(
        self,
        client: genai.Client,
        model: str,
        contents: List[genai_types.Content],
        gen_config: genai_types.GenerateContentConfig,
        request_id: str,
        timeout_seconds: int = 300,
        execution_id: str = "",
    ) -> AsyncIterator[pb.GenerateResponse]:
        """Stream from the Gemini API with timeout handling.

        While streaming, collects the original Content objects from each chunk
        (same pattern as the official SDK's Chat class). On successful
        completion, these are cached per execution_id so that subsequent
        Generate calls can replay them verbatim — preserving thought_signatures,
        thinking Parts, and all other fields.
        """
        has_content = False
        # Buffer usage info instead of yielding immediately. Usage-only
        # chunks are metadata, not content — yielding them would increment
        # chunks_yielded in generate(), preventing safe retries on empty streams.
        last_usage: Optional[pb.GenerateResponse] = None
        # Buffer grounding metadata (available on the candidate level,
        # typically on the last chunk of a streaming response).
        last_grounding_metadata = None
        # URL Context tool reports retrieved URLs on url_context_metadata, not always
        # in grounding_metadata.grounding_chunks — merge so the Go timeline can emit
        # url_context_result events.
        last_url_context_metadata = None
        # Collect original Content objects for caching (SDK Chat pattern).
        turn_contents: List[genai_types.Content] = []

        try:
            async with asyncio.timeout(timeout_seconds):
                stream = await client.aio.models.generate_content_stream(
                    model=model,
                    contents=contents,
                    config=gen_config,
                )
                async for chunk in stream:
                    if not chunk.candidates:
                        # Still check for usage on content-less chunks
                        if chunk.usage_metadata:
                            um = chunk.usage_metadata
                            last_usage = pb.GenerateResponse(
                                usage=pb.UsageInfo(
                                    input_tokens=um.prompt_token_count or 0,
                                    output_tokens=um.candidates_token_count or 0,
                                    total_tokens=um.total_token_count or 0,
                                    thinking_tokens=getattr(um, "thoughts_token_count", 0) or 0,
                                )
                            )
                        continue

                    candidate = chunk.candidates[0]

                    # Capture grounding_metadata when available
                    if hasattr(candidate, 'grounding_metadata') and candidate.grounding_metadata:
                        last_grounding_metadata = candidate.grounding_metadata
                    if hasattr(candidate, 'url_context_metadata') and candidate.url_context_metadata:
                        last_url_context_metadata = candidate.url_context_metadata

                    if not candidate.content or not candidate.content.parts:
                        # Still check for usage on content-less chunks
                        if chunk.usage_metadata:
                            um = chunk.usage_metadata
                            last_usage = pb.GenerateResponse(
                                usage=pb.UsageInfo(
                                    input_tokens=um.prompt_token_count or 0,
                                    output_tokens=um.candidates_token_count or 0,
                                    total_tokens=um.total_token_count or 0,
                                    thinking_tokens=getattr(um, "thoughts_token_count", 0) or 0,
                                )
                            )
                        continue

                    # Cache the original Content for replay in subsequent calls.
                    turn_contents.append(candidate.content)

                    for part in candidate.content.parts:
                        # Thinking content
                        if hasattr(part, "thought") and part.thought:
                            if hasattr(part, "text") and part.text:
                                has_content = True
                                yield pb.GenerateResponse(
                                    thinking=pb.ThinkingDelta(content=part.text)
                                )
                        # Function call
                        elif hasattr(part, "function_call") and part.function_call:
                            has_content = True
                            fc = part.function_call
                            args_str = json.dumps(dict(fc.args)) if fc.args else "{}"
                            call_id = str(uuid.uuid4())[:8]
                            yield pb.GenerateResponse(
                                tool_call=pb.ToolCallDelta(
                                    call_id=call_id,
                                    name=tool_name_from_api(fc.name),
                                    arguments=args_str,
                                )
                            )
                        # Code execution result
                        elif hasattr(part, "executable_code") and part.executable_code:
                            has_content = True
                            code = part.executable_code.code or ""
                            result = ""
                            yield pb.GenerateResponse(
                                code_execution=pb.CodeExecutionDelta(code=code, result=result)
                            )
                        elif hasattr(part, "code_execution_result") and part.code_execution_result:
                            has_content = True
                            result = part.code_execution_result.output or ""
                            yield pb.GenerateResponse(
                                code_execution=pb.CodeExecutionDelta(code="", result=result)
                            )
                        # Regular text
                        elif hasattr(part, "text") and part.text:
                            has_content = True
                            yield pb.GenerateResponse(
                                text=pb.TextDelta(content=part.text)
                            )

                    # Buffer usage info (will be yielded after content is confirmed)
                    if chunk.usage_metadata:
                        um = chunk.usage_metadata
                        last_usage = pb.GenerateResponse(
                            usage=pb.UsageInfo(
                                input_tokens=um.prompt_token_count or 0,
                                output_tokens=um.candidates_token_count or 0,
                                total_tokens=um.total_token_count or 0,
                                thinking_tokens=getattr(um, "thoughts_token_count", 0) or 0,
                            )
                        )

        except genai_errors.ServerError as exc:
            # 5xx errors from Google API are transient and should be retried
            raise _RetryableError(f"[{request_id}] Google API server error: {exc}") from exc

        except asyncio.TimeoutError as exc:
            raise _RetryableError(f"[{request_id}] Generation timed out after {timeout_seconds}s") from exc

        if not has_content:
            raise _RetryableError(f"[{request_id}] Empty response from LLM (no content generated)")

        # Cache model Content for this turn (only on success).
        if turn_contents and execution_id:
            self._cache_model_turn(execution_id, turn_contents)

        # Yield grounding (+ URL Context retrieval URLs) after content (before usage)
        combined_grounding = self._build_combined_grounding_response(
            last_grounding_metadata, last_url_context_metadata
        )
        if combined_grounding is not None:
            yield combined_grounding

        # Yield buffered usage info after confirming content was produced
        if last_usage is not None:
            yield last_usage

        # Final chunk
        yield pb.GenerateResponse(is_final=True)

    def _build_combined_grounding_response(
        self,
        grounding_metadata: Optional[Any],
        url_context_metadata: Optional[Any],
    ) -> Optional[pb.GenerateResponse]:
        """Build one GroundingDelta from grounding_metadata plus URL Context retrieval URLs."""
        if grounding_metadata is not None:
            resp = self._build_grounding_delta(grounding_metadata)
            delta = resp.grounding
        else:
            delta = pb.GroundingDelta()

        self._merge_url_context_metadata_into_delta(delta, url_context_metadata)

        if (
            not delta.web_search_queries
            and not delta.grounding_chunks
            and not delta.grounding_supports
            and not delta.search_entry_point_html
        ):
            return None
        return pb.GenerateResponse(grounding=delta)

    def _is_url_retrieval_success(self, status: Any) -> bool:
        """Match UrlRetrievalStatus from the GenAI SDK or raw string values."""
        if status is None:
            return False
        try:
            from google.genai.types import UrlRetrievalStatus

            if status == UrlRetrievalStatus.URL_RETRIEVAL_STATUS_SUCCESS:
                return True
        except Exception:
            pass
        val = getattr(status, "value", None)
        if val == "URL_RETRIEVAL_STATUS_SUCCESS":
            return True
        return "URL_RETRIEVAL_STATUS_SUCCESS" in str(status)

    def _merge_url_context_metadata_into_delta(
        self,
        delta: pb.GroundingDelta,
        ucm: Optional[Any],
    ) -> None:
        """Append successful URL Context retrievals as grounding chunk URIs (Go: url_context_result)."""
        if ucm is None:
            return
        url_entries = getattr(ucm, "url_metadata", None)
        if not url_entries:
            return

        existing_uris = {c.uri for c in delta.grounding_chunks if c.uri}

        for entry in url_entries:
            raw_url = getattr(entry, "retrieved_url", None)
            if raw_url is None:
                continue
            url = str(raw_url).strip()
            if not url:
                continue
            status = getattr(entry, "url_retrieval_status", None)
            if not self._is_url_retrieval_success(status):
                continue
            if url in existing_uris:
                continue
            delta.grounding_chunks.append(pb.GroundingChunkInfo(uri=url, title=url))
            existing_uris.add(url)

    def _build_grounding_delta(self, gm: "genai_types.GroundingMetadata") -> pb.GenerateResponse:
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
                    start_index=int(segment.start_index or 0) if segment else 0,
                    end_index=int(segment.end_index or 0) if segment else 0,
                    text=str(segment.text) if segment and hasattr(segment, 'text') and segment.text else "",
                )
                if hasattr(support, 'grounding_chunk_indices') and support.grounding_chunk_indices:
                    gs.grounding_chunk_indices.extend(support.grounding_chunk_indices)
                delta.grounding_supports.append(gs)

        # Search entry point HTML — extracted for completeness but not stored in timeline events (Q6)
        if hasattr(gm, 'search_entry_point') and gm.search_entry_point:
            if hasattr(gm.search_entry_point, 'rendered_content'):
                delta.search_entry_point_html = gm.search_entry_point.rendered_content or ""

        return pb.GenerateResponse(grounding=delta)


class _RetryableError(Exception):
    """Internal exception for retryable errors."""
    pass
