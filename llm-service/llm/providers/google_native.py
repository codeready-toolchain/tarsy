"""Google Gemini native provider using the google-genai SDK."""
import asyncio
import json
import logging
import os
import time
import uuid
from typing import AsyncIterator, Dict, List, Optional, Tuple

from google import genai
from google.genai import types as genai_types

from proto import llm_service_pb2 as pb
from llm.providers.base import LLMProvider

logger = logging.getLogger(__name__)

# Retry configuration
MAX_RETRIES = 3
RETRY_BACKOFF_BASE = 2  # seconds
EMPTY_RESPONSE_RETRY_DELAY = 3  # seconds

# Thought signature cache TTL
THOUGHT_SIGNATURE_TTL = 3600  # 1 hour


class GoogleNativeProvider(LLMProvider):
    """LLM provider using Google's native genai SDK.

    Features:
    - Cached SDK client (initialized once per API key)
    - Per-model thinking configuration
    - Thought signature caching per execution_id
    - Tool definition -> FunctionDeclaration conversion
    - Tool name conversion: server.tool <-> server__tool
    - Native tools (google_search, code_execution, url_context)
    - UsageInfo streaming
    - Transient retry logic
    - Error classification
    """

    def __init__(self):
        self._clients: Dict[str, genai.Client] = {}
        self._thought_signatures: Dict[str, Tuple[Optional[str], float]] = {}

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
        self, messages: List[pb.ConversationMessage]
    ) -> Tuple[Optional[str], List[genai_types.Content]]:
        """Convert proto messages to genai Contents.

        Returns (system_instruction, contents).
        System messages are extracted as system_instruction.
        Tool results (role=tool) are converted to FunctionResponse parts.
        """
        system_instruction = None
        contents: List[genai_types.Content] = []

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
                parts: List[genai_types.Part] = []
                if msg.content:
                    parts.append(genai_types.Part(text=msg.content))
                # Add function calls if present
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
                                name=self._tool_name_to_native(tc.name),
                                args=args,
                            )
                        )
                    )
                contents.append(
                    genai_types.Content(role="model", parts=parts)
                )
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
                                    name=self._tool_name_to_native(msg.tool_name),
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

    def _convert_tools(
        self, tools: List[pb.ToolDefinition], native_tools: Dict[str, bool]
    ) -> Optional[List[genai_types.Tool]]:
        """Convert proto tool definitions to genai Tool objects.

        If MCP tools are present, native tools are suppressed
        (mutual exclusivity per Gemini API constraint).
        """
        result_tools: List[genai_types.Tool] = []

        has_mcp_tools = len(tools) > 0

        # Add MCP tools as function declarations
        if has_mcp_tools:
            declarations = []
            for tool in tools:
                try:
                    params = json.loads(tool.parameters_schema) if tool.parameters_schema else {}
                except json.JSONDecodeError:
                    params = {}
                declarations.append(
                    genai_types.FunctionDeclaration(
                        name=self._tool_name_to_native(tool.name),
                        description=tool.description,
                        parameters=params if params else None,
                    )
                )
            result_tools.append(genai_types.Tool(function_declarations=declarations))

        # Add native tools (only when no MCP tools)
        if not has_mcp_tools and native_tools:
            if native_tools.get("google_search"):
                result_tools.append(genai_types.Tool(google_search=genai_types.GoogleSearch()))
            if native_tools.get("code_execution"):
                result_tools.append(genai_types.Tool(code_execution=genai_types.ToolCodeExecution()))
            if native_tools.get("url_context"):
                result_tools.append(genai_types.Tool(url_context=genai_types.ToolUrlContext()))

        return result_tools if result_tools else None

    @staticmethod
    def _tool_name_to_native(name: str) -> str:
        """Convert canonical 'server.tool' format to 'server__tool' for Gemini API.

        The mapping uses '__' as the dot replacement. Tool name segments
        (the parts between dots) must not contain '__' themselves, or the
        round-trip would be lossy. We validate and reject such names early.
        """
        for segment in name.split("."):
            if "__" in segment:
                raise ValueError(
                    f"Tool name segment '{segment}' in '{name}' contains '__' "
                    f"which conflicts with the dot separator encoding. "
                    f"Rename the tool to avoid double underscores."
                )
        return name.replace(".", "__")

    @staticmethod
    def _tool_name_from_native(name: str) -> str:
        """Convert 'server__tool' back to canonical 'server.tool' format."""
        return name.replace("__", ".")

    def _get_cached_thought_signature(self, execution_id: str) -> Optional[str]:
        """Retrieve cached thought signature for an execution_id."""
        entry = self._thought_signatures.get(execution_id)
        if entry is None:
            return None
        sig, ts = entry
        if time.time() - ts > THOUGHT_SIGNATURE_TTL:
            del self._thought_signatures[execution_id]
            return None
        return sig

    def _cache_thought_signature(self, execution_id: str, signature: Optional[str]) -> None:
        """Cache thought signature for an execution_id."""
        self._thought_signatures[execution_id] = (signature, time.time())
        # Evict old entries
        now = time.time()
        expired = [k for k, (_, ts) in self._thought_signatures.items() if now - ts > THOUGHT_SIGNATURE_TTL]
        for k in expired:
            del self._thought_signatures[k]

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

        try:
            system_instruction, contents = self._convert_messages(list(request.messages))
            native_tools = dict(config.native_tools) if config.native_tools else {}
            tools = self._convert_tools(list(request.tools), native_tools)
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

        # Retry loop.
        # Only retry when zero chunks have been yielded to the caller.
        # Once any chunk is yielded over gRPC it cannot be retracted,
        # so retrying after partial output would corrupt the stream.
        last_error = None
        for attempt in range(MAX_RETRIES):
            chunks_yielded = 0
            try:
                async for chunk in self._stream_with_timeout(
                    client, config.model, contents, gen_config, request_id
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
        timeout_seconds: int = 180,
    ) -> AsyncIterator[pb.GenerateResponse]:
        """Stream from the Gemini API with timeout handling."""
        has_content = False
        # Buffer usage info instead of yielding immediately. Usage-only
        # chunks are metadata, not content — yielding them would increment
        # chunks_yielded in generate(), preventing safe retries on empty streams.
        last_usage: Optional[pb.GenerateResponse] = None
        # Buffer grounding metadata (available on the candidate level,
        # typically on the last chunk of a streaming response).
        last_grounding_metadata = None

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
                                    thinking_tokens=getattr(um, "thinking_token_count", 0) or 0,
                                )
                            )
                        continue

                    candidate = chunk.candidates[0]

                    # Capture grounding_metadata when available
                    if hasattr(candidate, 'grounding_metadata') and candidate.grounding_metadata:
                        last_grounding_metadata = candidate.grounding_metadata

                    if not candidate.content or not candidate.content.parts:
                        # Still check for usage on content-less chunks
                        if chunk.usage_metadata:
                            um = chunk.usage_metadata
                            last_usage = pb.GenerateResponse(
                                usage=pb.UsageInfo(
                                    input_tokens=um.prompt_token_count or 0,
                                    output_tokens=um.candidates_token_count or 0,
                                    total_tokens=um.total_token_count or 0,
                                    thinking_tokens=getattr(um, "thinking_token_count", 0) or 0,
                                )
                            )
                        continue

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
                                    name=self._tool_name_from_native(fc.name),
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
                                thinking_tokens=getattr(um, "thinking_token_count", 0) or 0,
                            )
                        )

        except asyncio.TimeoutError as exc:
            raise _RetryableError(f"[{request_id}] Generation timed out after {timeout_seconds}s") from exc

        if not has_content:
            raise _RetryableError(f"[{request_id}] Empty response from LLM (no content generated)")

        # Yield grounding metadata after content (before usage)
        if last_grounding_metadata is not None:
            yield self._build_grounding_delta(last_grounding_metadata)

        # Yield buffered usage info after confirming content was produced
        if last_usage is not None:
            yield last_usage

        # Final chunk
        yield pb.GenerateResponse(is_final=True)


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
