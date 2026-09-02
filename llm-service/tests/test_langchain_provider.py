"""Tests for LangChainProvider."""
import asyncio
import os
import pytest
from unittest.mock import AsyncMock, MagicMock, patch
import httpx

from langchain_core.messages import (
    AIMessage,
    AIMessageChunk,
    HumanMessage,
    SystemMessage,
    ToolMessage,
)

from llm_proto import llm_service_pb2 as pb
from llm.providers import prompt_cache
from llm.providers.langchain_provider import LangChainProvider, _RetryableError

pytestmark = pytest.mark.unit


@pytest.fixture
def provider():
    """Create a LangChainProvider instance."""
    return LangChainProvider()


class TestLangChainProviderMessageConversion:
    """Test message conversion from proto to LangChain."""

    def test_system_message(self, provider):
        messages = [pb.ConversationMessage(role="system", content="You are helpful")]
        result = provider._convert_messages(messages)
        assert len(result) == 1
        assert isinstance(result[0], SystemMessage)
        assert result[0].content == "You are helpful"

    def test_user_message(self, provider):
        messages = [pb.ConversationMessage(role="user", content="Hello")]
        result = provider._convert_messages(messages)
        assert len(result) == 1
        assert isinstance(result[0], HumanMessage)
        assert result[0].content == "Hello"

    def test_assistant_message_text_only(self, provider):
        messages = [pb.ConversationMessage(role="assistant", content="Hi there")]
        result = provider._convert_messages(messages)
        assert len(result) == 1
        assert isinstance(result[0], AIMessage)
        assert result[0].content == "Hi there"
        assert result[0].tool_calls == []

    def test_assistant_message_with_tool_calls(self, provider):
        messages = [
            pb.ConversationMessage(
                role="assistant",
                content="Let me check",
                tool_calls=[
                    pb.ToolCall(id="tc1", name="server.tool", arguments='{"arg": "value"}'),
                ],
            ),
        ]
        result = provider._convert_messages(messages)
        assert len(result) == 1
        assert isinstance(result[0], AIMessage)
        assert result[0].content == "Let me check"
        assert len(result[0].tool_calls) == 1
        assert result[0].tool_calls[0]["id"] == "tc1"
        assert result[0].tool_calls[0]["name"] == "server__tool"
        assert result[0].tool_calls[0]["args"] == {"arg": "value"}

    def test_tool_message(self, provider):
        messages = [
            pb.ConversationMessage(
                role="tool",
                tool_call_id="tc1",
                tool_name="server.tool",
                content='{"result": "success"}',
            ),
        ]
        result = provider._convert_messages(messages)
        assert len(result) == 1
        assert isinstance(result[0], ToolMessage)
        assert result[0].content == '{"result": "success"}'
        assert result[0].tool_call_id == "tc1"
        assert result[0].name == "server__tool"

    def test_unknown_role_raises(self, provider):
        messages = [pb.ConversationMessage(role="unknown", content="test")]
        with pytest.raises(ValueError, match="Unrecognized message role 'unknown'"):
            provider._convert_messages(messages)

    def test_assistant_invalid_json_tool_args(self, provider):
        """Test that invalid JSON in tool call args falls back to empty dict."""
        messages = [
            pb.ConversationMessage(
                role="assistant",
                content="",
                tool_calls=[pb.ToolCall(id="tc1", name="server.tool", arguments="not-json")],
            ),
        ]
        result = provider._convert_messages(messages)
        assert result[0].tool_calls[0]["args"] == {}

    def test_full_conversation(self, provider):
        messages = [
            pb.ConversationMessage(role="system", content="Be helpful"),
            pb.ConversationMessage(role="user", content="What is 2+2?"),
            pb.ConversationMessage(role="assistant", content="4"),
            pb.ConversationMessage(role="user", content="Thanks"),
        ]
        result = provider._convert_messages(messages)
        assert len(result) == 4
        assert isinstance(result[0], SystemMessage)
        assert isinstance(result[1], HumanMessage)
        assert isinstance(result[2], AIMessage)
        assert isinstance(result[3], HumanMessage)


class TestLangChainProviderToolBinding:
    """Test tool binding."""

    def test_bind_tools_creates_function_schema(self):
        mock_model = MagicMock()
        mock_model.bind_tools.return_value = mock_model
        tools = [
            pb.ToolDefinition(
                name="server.read",
                description="Read a file",
                parameters_schema='{"type": "object", "properties": {"path": {"type": "string"}}}',
            ),
        ]
        result = LangChainProvider._bind_tools(mock_model, tools)
        mock_model.bind_tools.assert_called_once()
        bound_tools = mock_model.bind_tools.call_args[0][0]
        assert len(bound_tools) == 1
        assert bound_tools[0]["type"] == "function"
        assert bound_tools[0]["function"]["name"] == "server__read"
        assert bound_tools[0]["function"]["description"] == "Read a file"

    def test_bind_tools_empty_list_returns_model(self):
        mock_model = MagicMock()
        result = LangChainProvider._bind_tools(mock_model, [])
        mock_model.bind_tools.assert_not_called()
        assert result is mock_model

    @patch("llm.providers.langchain_provider.LangChainProvider._create_chat_model")
    def test_get_or_create_model_skips_tools_on_image_model(self, mock_create, provider):
        mock_model = MagicMock()
        mock_create.return_value = mock_model
        config = pb.LLMConfig(
            provider="google",
            model="gemini-3.1-flash-image",
            api_key_env="GOOGLE_API_KEY",
        )
        tools = [pb.ToolDefinition(name="server.read", description="Read a file")]

        result = provider._get_or_create_model(config, tools)

        mock_model.bind_tools.assert_not_called()
        assert result is mock_model


class TestLangChainProviderReasoningConfig:
    """Test reasoning/thinking configuration helpers."""

    def test_google_thinking_gemini_2_5_pro(self):
        result = LangChainProvider._get_google_thinking_kwargs("gemini-2.5-pro-preview")
        assert result == {"include_thoughts": True, "thinking_budget": 32768}

    def test_google_thinking_gemini_2_5_flash(self):
        result = LangChainProvider._get_google_thinking_kwargs("gemini-2.5-flash")
        assert result == {"include_thoughts": True, "thinking_budget": 24576}

    def test_google_thinking_gemini_3(self):
        result = LangChainProvider._get_google_thinking_kwargs("gemini-3-flash-preview")
        assert result == {"include_thoughts": True, "thinking_level": "high"}

    # --- OpenAI: reasoning enabled by default ---
    @pytest.mark.parametrize("model", [
        "o3", "o4-mini", "gpt-5", "gpt-5-mini", "gpt-5-nano",
        "gpt-5-thinking", "gpt-6-turbo",
    ])
    def test_openai_reasoning(self, model):
        result = LangChainProvider._get_openai_reasoning_kwargs(model)
        assert result["use_responses_api"] is True
        assert result["reasoning"]["effort"] == "high"
        assert result["reasoning"]["summary"] == "auto"

    # --- OpenAI: non-reasoning GPT-5 variants ---
    @pytest.mark.parametrize("model", ["gpt-5-chat-latest", "gpt-5-main-mini"])
    def test_openai_no_reasoning(self, model):
        assert LangChainProvider._get_openai_reasoning_kwargs(model) == {}

    # --- Anthropic: manual budget-based thinking for pre-5th-gen models ---
    @pytest.mark.parametrize("model", [
        "claude-sonnet-4-5-20250929", "claude-opus-4-6",
        "claude-haiku-4-5-20251001", "claude-sonnet-4-6-20260217",
    ])
    def test_anthropic_thinking(self, model):
        result = LangChainProvider._get_anthropic_thinking_kwargs(model)
        assert result["thinking"]["type"] == "enabled"
        assert result["thinking"]["budget_tokens"] == 32000
        assert result["max_tokens"] == 64000

    # --- Anthropic: 5th-gen models only support adaptive thinking (manual
    # budget_tokens returns a 400 error on these) ---
    @pytest.mark.parametrize("model", [
        "claude-sonnet-5", "claude-sonnet-5-20260101",
        "claude-opus-5", "claude-fable-5",
    ])
    def test_anthropic_adaptive_thinking(self, model):
        result = LangChainProvider._get_anthropic_thinking_kwargs(model)
        assert result["thinking"] == {"type": "adaptive"}
        assert result["max_tokens"] == 64000



class TestLangChainProviderModelCreation:
    """Test model creation for different providers."""

    @patch.dict(os.environ, {"OPENAI_API_KEY": "test-key"})
    @patch("llm.providers.langchain_provider.LangChainProvider._create_chat_model")
    def test_get_or_create_model_caches(self, mock_create, provider):
        mock_model = MagicMock()
        mock_create.return_value = mock_model
        config = pb.LLMConfig(provider="openai", model="o4-mini", api_key_env="OPENAI_API_KEY")

        model1 = provider._get_or_create_model(config, [])
        model2 = provider._get_or_create_model(config, [])

        mock_create.assert_called_once()

    @patch.dict(os.environ, {"OPENAI_API_KEY": "test-key"})
    def test_create_openai_model(self, provider):
        with patch("llm.providers.langchain_provider.ChatOpenAI", create=True) as MockChat:
            from langchain_openai import ChatOpenAI
            config = pb.LLMConfig(provider="openai", model="o4-mini", api_key_env="OPENAI_API_KEY")
            model = provider._create_chat_model(config)
            assert model is not None

    @patch.dict(os.environ, {"ANTHROPIC_API_KEY": "test-key"})
    def test_create_anthropic_model(self, provider):
        config = pb.LLMConfig(provider="anthropic", model="claude-sonnet-4-5-20250929", api_key_env="ANTHROPIC_API_KEY")
        model = provider._create_chat_model(config)
        assert model is not None

    @patch.dict(os.environ, {}, clear=True)
    def test_create_unsupported_provider_raises_even_without_key(self, provider):
        """Unsupported provider raises provider error regardless of key."""
        config = pb.LLMConfig(provider="unsupported", model="model-1", api_key_env="MISSING_KEY")
        with pytest.raises(ValueError, match="Unsupported provider 'unsupported'"):
            provider._create_chat_model(config)

    @patch.dict(os.environ, {}, clear=True)
    def test_create_model_missing_api_key(self, provider):
        config = pb.LLMConfig(provider="openai", model="o4-mini", api_key_env="MISSING_KEY")
        with pytest.raises(ValueError, match="not set"):
            provider._create_chat_model(config)

    def test_create_unsupported_provider_with_key(self, provider):
        """Test that unsupported provider raises even when key is available."""
        with patch.dict(os.environ, {"SOME_KEY": "value"}):
            config = pb.LLMConfig(provider="unsupported", model="model-1", api_key_env="SOME_KEY")
            with pytest.raises(ValueError, match="Unsupported provider 'unsupported'"):
                provider._create_chat_model(config)


class TestLangChainProviderStreaming:
    """Test streaming response handling."""

    @pytest.mark.asyncio
    async def test_stream_text_content(self, provider):
        """Test streaming with plain text content."""
        chunk = AIMessageChunk(content="Hello, world!")
        chunk.usage_metadata = None

        async def mock_astream(messages):
            yield chunk

        class MockModel:
            def astream(self, messages):
                return mock_astream(messages)

        mock_model = MockModel()

        responses = []
        async for resp in provider._stream_response(mock_model, [], "test-req"):
            responses.append(resp)

        text_responses = [r for r in responses if r.HasField("text")]
        assert len(text_responses) == 1
        assert text_responses[0].text.content == "Hello, world!"
        assert responses[-1].is_final

    @pytest.mark.asyncio
    async def test_stream_tool_call_chunks(self, provider):
        """Test streaming with progressive tool call chunks."""
        chunk1 = AIMessageChunk(content="")
        chunk1.tool_call_chunks = [
            {"index": 0, "name": "server__read", "id": "call-1", "args": '{"pa'},
        ]
        chunk1.usage_metadata = None

        chunk2 = AIMessageChunk(content="")
        chunk2.tool_call_chunks = [
            {"index": 0, "args": 'th": "/tmp"}'},
        ]
        chunk2.usage_metadata = None

        async def mock_astream(messages):
            yield chunk1
            yield chunk2

        class MockModel:
            def astream(self, messages):
                return mock_astream(messages)

        mock_model = MockModel()

        responses = []
        async for resp in provider._stream_response(mock_model, [], "test-req"):
            responses.append(resp)

        tool_responses = [r for r in responses if r.HasField("tool_call")]
        assert len(tool_responses) == 1
        assert tool_responses[0].tool_call.name == "server.read"
        assert tool_responses[0].tool_call.call_id == "call-1"
        assert tool_responses[0].tool_call.arguments == '{"path": "/tmp"}'

    @pytest.mark.asyncio
    async def test_stream_reasoning_via_content_blocks(self, provider):
        """Test streaming with reasoning content blocks."""
        # content_blocks is a read-only property on AIMessageChunk,
        # so we mock the chunk object to control its value.
        mock_chunk = MagicMock(spec=AIMessageChunk)
        mock_chunk.content_blocks = [
            {"type": "reasoning", "reasoning": "Let me think about this..."},
            {"type": "text", "text": "The answer is 42."},
        ]
        mock_chunk.content = ""
        mock_chunk.tool_call_chunks = []
        mock_chunk.usage_metadata = None
        # Make isinstance check pass
        mock_chunk.__class__ = AIMessageChunk

        async def mock_astream(messages):
            yield mock_chunk

        class MockModel:
            def astream(self, messages):
                return mock_astream(messages)

        mock_model = MockModel()

        responses = []
        async for resp in provider._stream_response(mock_model, [], "test-req"):
            responses.append(resp)

        thinking_responses = [r for r in responses if r.HasField("thinking")]
        text_responses = [r for r in responses if r.HasField("text")]
        assert len(thinking_responses) == 1
        assert thinking_responses[0].thinking.content == "Let me think about this..."
        assert len(text_responses) == 1
        assert text_responses[0].text.content == "The answer is 42."

    @pytest.mark.asyncio
    async def test_stream_anthropic_thinking_blocks(self, provider):
        """Test streaming Anthropic thinking (type='thinking' wrapped as non_standard)."""
        mock_chunk = MagicMock(spec=AIMessageChunk)
        mock_chunk.content_blocks = [
            {"type": "non_standard", "value": {"type": "thinking", "thinking": "Let me reason..."}},
            {"type": "text", "text": "The answer is 42."},
        ]
        mock_chunk.content = ""
        mock_chunk.tool_call_chunks = []
        mock_chunk.usage_metadata = None
        mock_chunk.additional_kwargs = {}
        mock_chunk.__class__ = AIMessageChunk

        async def mock_astream(messages):
            yield mock_chunk

        class MockModel:
            def astream(self, messages):
                return mock_astream(messages)

        responses = []
        async for resp in provider._stream_response(MockModel(), [], "test-req"):
            responses.append(resp)

        thinking_responses = [r for r in responses if r.HasField("thinking")]
        text_responses = [r for r in responses if r.HasField("text")]
        assert len(thinking_responses) == 1
        assert thinking_responses[0].thinking.content == "Let me reason..."
        assert len(text_responses) == 1
        assert text_responses[0].text.content == "The answer is 42."

    @pytest.mark.asyncio
    async def test_stream_openai_reasoning_summary_chunk(self, provider):
        """Test streaming OpenAI reasoning via additional_kwargs (Responses API)."""
        mock_chunk = MagicMock(spec=AIMessageChunk)
        mock_chunk.content_blocks = []
        mock_chunk.content = ""
        mock_chunk.tool_call_chunks = []
        mock_chunk.usage_metadata = None
        mock_chunk.additional_kwargs = {"reasoning_summary_chunk": "Step 1: analyze the problem..."}
        mock_chunk.__class__ = AIMessageChunk

        text_chunk = AIMessageChunk(content="The answer is 42.")
        text_chunk.usage_metadata = None

        async def mock_astream(messages):
            yield mock_chunk
            yield text_chunk

        class MockModel:
            def astream(self, messages):
                return mock_astream(messages)

        responses = []
        async for resp in provider._stream_response(MockModel(), [], "test-req"):
            responses.append(resp)

        thinking_responses = [r for r in responses if r.HasField("thinking")]
        text_responses = [r for r in responses if r.HasField("text")]
        assert len(thinking_responses) == 1
        assert thinking_responses[0].thinking.content == "Step 1: analyze the problem..."
        assert len(text_responses) == 1
        assert text_responses[0].text.content == "The answer is 42."

    @pytest.mark.asyncio
    async def test_stream_usage_accumulates_across_chunks(self, provider):
        """Test that usage metadata is accumulated across multiple streaming chunks."""
        chunk1 = AIMessageChunk(content="Hello")
        chunk1.usage_metadata = {"input_tokens": 100, "output_tokens": 0, "total_tokens": 100}

        chunk2 = AIMessageChunk(content=" world")
        chunk2.usage_metadata = {"input_tokens": 0, "output_tokens": 50, "total_tokens": 50}

        chunk3 = AIMessageChunk(content="")
        chunk3.usage_metadata = {"input_tokens": 0, "output_tokens": 30, "total_tokens": 30}

        async def mock_astream(messages):
            yield chunk1
            yield chunk2
            yield chunk3

        class MockModel:
            def astream(self, messages):
                return mock_astream(messages)

        responses = []
        async for resp in provider._stream_response(MockModel(), [], "test-req"):
            responses.append(resp)

        usage_responses = [r for r in responses if r.HasField("usage")]
        assert len(usage_responses) == 1
        assert usage_responses[0].usage.input_tokens == 100
        assert usage_responses[0].usage.output_tokens == 80
        assert usage_responses[0].usage.total_tokens == 180
        assert usage_responses[0].usage.cache_read_tokens == 0
        assert usage_responses[0].usage.cache_creation_tokens == 0

    @pytest.mark.asyncio
    async def test_stream_cache_tokens_last_wins_not_wiped_by_output_only_chunk(self, provider):
        """Cache fields last-win when reported; later output-only usage does not wipe them."""
        chunk1 = AIMessageChunk(content="Hello")
        chunk1.usage_metadata = {
            "input_tokens": 100,
            "output_tokens": 0,
            "total_tokens": 100,
            "input_token_details": {"cache_read": 70, "cache_creation": 20},
        }

        chunk2 = AIMessageChunk(content=" world")
        chunk2.usage_metadata = {"input_tokens": 0, "output_tokens": 50, "total_tokens": 50}

        async def mock_astream(messages):
            yield chunk1
            yield chunk2

        class MockModel:
            def astream(self, messages):
                return mock_astream(messages)

        responses = []
        async for resp in provider._stream_response(MockModel(), [], "test-req"):
            responses.append(resp)

        usage_responses = [r for r in responses if r.HasField("usage")]
        assert len(usage_responses) == 1
        assert usage_responses[0].usage.input_tokens == 10
        assert usage_responses[0].usage.output_tokens == 50
        assert usage_responses[0].usage.cache_read_tokens == 70
        assert usage_responses[0].usage.cache_creation_tokens == 20

    @pytest.mark.asyncio
    async def test_stream_cache_tokens_last_wins_across_reported_chunks(self, provider):
        chunk1 = AIMessageChunk(content="a")
        chunk1.usage_metadata = {
            "input_tokens": 10,
            "output_tokens": 1,
            "total_tokens": 11,
            "input_token_details": {"cache_read": 8},
        }
        chunk2 = AIMessageChunk(content="b")
        chunk2.usage_metadata = {
            "input_tokens": 0,
            "output_tokens": 2,
            "total_tokens": 2,
            "input_token_details": {"cache_read": 9, "cache_creation": 1},
        }

        async def mock_astream(messages):
            yield chunk1
            yield chunk2

        class MockModel:
            def astream(self, messages):
                return mock_astream(messages)

        responses = []
        async for resp in provider._stream_response(MockModel(), [], "test-req"):
            responses.append(resp)

        usage = [r for r in responses if r.HasField("usage")]
        assert usage[0].usage.input_tokens == 0
        assert usage[0].usage.output_tokens == 3
        assert usage[0].usage.cache_read_tokens == 9
        assert usage[0].usage.cache_creation_tokens == 1

    @pytest.mark.asyncio
    async def test_stream_emits_usage_from_response_metadata_cache_only(self, provider):
        """Cache fields in response_metadata still yield UsageInfo when usage_metadata is None."""
        chunk = AIMessageChunk(content="Response text")
        chunk.usage_metadata = None
        chunk.response_metadata = {
            "usage": {
                "cache_read_input_tokens": 40,
                "cache_creation_input_tokens": 10,
            }
        }

        async def mock_astream(messages):
            yield chunk

        class MockModel:
            def astream(self, messages):
                return mock_astream(messages)

        responses = []
        async for resp in provider._stream_response(MockModel(), [], "test-req"):
            responses.append(resp)

        usage_responses = [r for r in responses if r.HasField("usage")]
        assert len(usage_responses) == 1
        assert usage_responses[0].usage.input_tokens == 0
        assert usage_responses[0].usage.output_tokens == 0
        assert usage_responses[0].usage.cache_read_tokens == 40
        assert usage_responses[0].usage.cache_creation_tokens == 10

    @pytest.mark.asyncio
    async def test_stream_anthropic_raw_input_is_already_uncached(self, provider):
        """Anthropic raw usage.input_tokens is uncached; do not subtract from LC inclusive."""
        chunk = AIMessageChunk(content="Response text")
        chunk.usage_metadata = {
            "input_tokens": 100,
            "output_tokens": 5,
            "total_tokens": 105,
            "input_token_details": {"cache_read": 40, "cache_creation": 0},
        }
        chunk.response_metadata = {
            "usage": {
                "input_tokens": 20,
                "cache_read_input_tokens": 40,
                "cache_creation_input_tokens": 80,
            }
        }

        async def mock_astream(messages):
            yield chunk

        class MockModel:
            def astream(self, messages):
                return mock_astream(messages)

        responses = []
        async for resp in provider._stream_response(MockModel(), [], "test-req"):
            responses.append(resp)

        usage_responses = [r for r in responses if r.HasField("usage")]
        assert len(usage_responses) == 1
        assert usage_responses[0].usage.input_tokens == 20
        assert usage_responses[0].usage.cache_read_tokens == 40
        assert usage_responses[0].usage.cache_creation_tokens == 80

    @pytest.mark.asyncio
    async def test_stream_usage_metadata(self, provider):
        """Test that usage metadata is buffered and yielded after content."""
        chunk = AIMessageChunk(content="Response text")
        chunk.usage_metadata = {
            "input_tokens": 10,
            "output_tokens": 20,
            "total_tokens": 30,
        }

        async def mock_astream(messages):
            yield chunk

        class MockModel:
            def astream(self, messages):
                return mock_astream(messages)

        mock_model = MockModel()

        responses = []
        async for resp in provider._stream_response(mock_model, [], "test-req"):
            responses.append(resp)

        usage_responses = [r for r in responses if r.HasField("usage")]
        assert len(usage_responses) == 1
        assert usage_responses[0].usage.input_tokens == 10
        assert usage_responses[0].usage.output_tokens == 20
        assert usage_responses[0].usage.total_tokens == 30

    @pytest.mark.asyncio
    async def test_stream_gemini_list_content_thinking_and_text(self, provider):
        """Test Gemini list-content fallback: thinking + text from chunk.content list."""
        chunk = AIMessageChunk(content=[
            {"type": "thinking", "thinking": "Let me reason..."},
            {"type": "text", "text": "The answer."},
        ])
        chunk.usage_metadata = None

        async def mock_astream(messages):
            yield chunk

        class MockModel:
            def astream(self, messages):
                return mock_astream(messages)

        responses = []
        async for resp in provider._stream_response(MockModel(), [], "test-req"):
            responses.append(resp)

        thinking = [r for r in responses if r.HasField("thinking")]
        text = [r for r in responses if r.HasField("text")]
        assert len(thinking) == 1
        assert thinking[0].thinking.content == "Let me reason..."
        assert len(text) == 1
        assert text[0].text.content == "The answer."

    @pytest.mark.asyncio
    async def test_stream_usage_metadata_as_object(self, provider):
        """Test usage_metadata accessed via getattr (non-dict, e.g. NamedTuple)."""
        usage_obj = MagicMock()
        usage_obj.input_tokens = 50
        usage_obj.output_tokens = 25
        usage_obj.total_tokens = 75

        chunk = AIMessageChunk(content="Response")
        chunk.usage_metadata = usage_obj

        async def mock_astream(messages):
            yield chunk

        class MockModel:
            def astream(self, messages):
                return mock_astream(messages)

        responses = []
        async for resp in provider._stream_response(MockModel(), [], "test-req"):
            responses.append(resp)

        usage = [r for r in responses if r.HasField("usage")]
        assert len(usage) == 1
        assert usage[0].usage.input_tokens == 50
        assert usage[0].usage.output_tokens == 25
        assert usage[0].usage.total_tokens == 75

    @pytest.mark.asyncio
    async def test_stream_empty_response_raises_retryable(self, provider):
        """Test that empty response raises _RetryableError."""
        async def mock_astream(messages):
            return
            yield  # make it an async generator

        class MockModel:
            def astream(self, messages):
                return mock_astream(messages)

        mock_model = MockModel()

        with pytest.raises(_RetryableError, match="Empty response"):
            async for _ in provider._stream_response(mock_model, [], "test-req"):
                pass


class TestLangChainProviderGenerate:
    """Test the full generate flow."""

    @pytest.mark.asyncio
    async def test_generate_missing_api_key(self, provider):
        """Test that generate yields error when API key env var is missing."""
        with patch.dict(os.environ, {}, clear=True):
            request = pb.GenerateRequest(
                session_id="sess-1",
                execution_id="exec-1",
                llm_config=pb.LLMConfig(
                    backend="langchain",
                    provider="openai",
                    model="o4-mini",
                    api_key_env="MISSING_KEY",
                ),
                messages=[],
            )

            responses = []
            async for resp in provider.generate(request):
                responses.append(resp)

            assert len(responses) == 1
            assert responses[0].HasField("error")
            assert responses[0].error.code == "credentials"
            assert responses[0].is_final

    @pytest.mark.asyncio
    @patch.dict(os.environ, {"TEST_KEY": "test-value"})
    async def test_generate_success(self, provider):
        """Test successful generate with mocked model."""
        chunk = AIMessageChunk(content="Hello!")
        chunk.usage_metadata = None

        async def mock_astream(messages):
            yield chunk

        class MockModel:
            def astream(self, messages):
                return mock_astream(messages)

        mock_model = MockModel()

        with patch.object(provider, "_get_or_create_model", return_value=mock_model):
            request = pb.GenerateRequest(
                session_id="sess-1",
                execution_id="exec-1",
                llm_config=pb.LLMConfig(
                    backend="langchain",
                    provider="openai",
                    model="o4-mini",
                    api_key_env="TEST_KEY",
                ),
                messages=[pb.ConversationMessage(role="user", content="Hi")],
            )

            responses = []
            async for resp in provider.generate(request):
                responses.append(resp)

            assert len(responses) == 2
            assert responses[0].HasField("text")
            assert responses[0].text.content == "Hello!"
            assert responses[1].is_final

    @pytest.mark.asyncio
    @patch.dict(os.environ, {"TEST_KEY": "test-value"})
    @patch("asyncio.sleep", new_callable=AsyncMock)
    async def test_generate_retries_on_empty_stream(self, mock_sleep, provider):
        """Test that retries happen when zero chunks were produced."""
        call_count = 0

        chunk = AIMessageChunk(content="Success after retry")
        chunk.usage_metadata = None

        async def empty_stream(messages):
            # Yield nothing — triggers "Empty response" RetryableError
            return
            yield  # make it an async generator

        async def good_stream(messages):
            yield chunk

        class MockModel:
            """Mock model where astream is a regular method returning an async generator."""
            def astream(self, messages):
                nonlocal call_count
                call_count += 1
                if call_count == 1:
                    return empty_stream(messages)
                return good_stream(messages)

        mock_model = MockModel()

        with patch.object(provider, "_get_or_create_model", return_value=mock_model):
            request = pb.GenerateRequest(
                session_id="sess-1",
                execution_id="exec-1",
                llm_config=pb.LLMConfig(
                    backend="langchain",
                    provider="openai",
                    model="o4-mini",
                    api_key_env="TEST_KEY",
                ),
                messages=[pb.ConversationMessage(role="user", content="Hi")],
            )

            responses = []
            async for resp in provider.generate(request):
                responses.append(resp)

            assert call_count == 2
            text_responses = [r for r in responses if r.HasField("text")]
            assert len(text_responses) == 1
            assert text_responses[0].text.content == "Success after retry"

    @pytest.mark.asyncio
    @patch.dict(os.environ, {"TEST_KEY": "test-value"})
    async def test_generate_no_retry_after_partial_output(self, provider):
        """Test that no retry happens when chunks were already yielded."""
        call_count = 0

        async def mock_stream_partial(messages):
            nonlocal call_count
            call_count += 1
            yield AIMessageChunk(content="Partial data")
            raise _RetryableError("timeout after partial output")

        class MockModel:
            def astream(self, messages):
                return mock_stream_partial(messages)

        mock_model = MockModel()

        with patch.object(provider, "_get_or_create_model", return_value=mock_model):
            request = pb.GenerateRequest(
                session_id="sess-1",
                execution_id="exec-1",
                llm_config=pb.LLMConfig(
                    backend="langchain",
                    provider="openai",
                    model="o4-mini",
                    api_key_env="TEST_KEY",
                ),
                messages=[pb.ConversationMessage(role="user", content="Hi")],
            )

            responses = []
            async for resp in provider.generate(request):
                responses.append(resp)

            assert call_count == 1
            assert responses[0].HasField("text")
            assert responses[0].text.content == "Partial data"
            assert responses[1].HasField("error")
            assert responses[1].error.code == "partial_stream_error"
            assert responses[1].is_final

    @pytest.mark.asyncio
    @patch.dict(os.environ, {"TEST_KEY": "test-value"})
    async def test_generate_unsupported_provider(self, provider):
        """Test that unsupported provider yields error."""
        request = pb.GenerateRequest(
            session_id="sess-1",
            execution_id="exec-1",
            llm_config=pb.LLMConfig(
                backend="langchain",
                provider="unsupported",
                model="model-1",
                api_key_env="TEST_KEY",
            ),
            messages=[],
        )

        responses = []
        async for resp in provider.generate(request):
            responses.append(resp)

        assert len(responses) == 1
        assert responses[0].HasField("error")
        assert "Unsupported provider" in responses[0].error.message
        assert responses[0].error.code == "invalid_request"
        assert responses[0].is_final

    @pytest.mark.asyncio
    @patch.dict(os.environ, {"TEST_KEY": "test-value"})
    async def test_generate_invalid_messages(self, provider):
        """Test that invalid messages yield error before streaming."""
        with patch.object(provider, "_get_or_create_model", return_value=MagicMock()):
            request = pb.GenerateRequest(
                session_id="sess-1",
                execution_id="exec-1",
                llm_config=pb.LLMConfig(
                    backend="langchain",
                    provider="openai",
                    model="o4-mini",
                    api_key_env="TEST_KEY",
                ),
                messages=[pb.ConversationMessage(role="bad_role", content="test")],
            )

            responses = []
            async for resp in provider.generate(request):
                responses.append(resp)

            assert len(responses) == 1
            assert responses[0].HasField("error")
            assert responses[0].error.code == "invalid_request"
            assert "Unrecognized message role" in responses[0].error.message

    @pytest.mark.asyncio
    @patch.dict(os.environ, {"TEST_KEY": "test-value"})
    async def test_generate_non_retryable_exception(self, provider):
        """Test that non-retryable exceptions yield provider_error."""
        async def exploding_stream(messages):
            raise RuntimeError("Something unexpected")
            yield  # make it an async generator

        class ExplodingModel:
            def astream(self, messages):
                return exploding_stream(messages)

        with patch.object(provider, "_get_or_create_model", return_value=ExplodingModel()):
            request = pb.GenerateRequest(
                session_id="sess-1",
                execution_id="exec-1",
                llm_config=pb.LLMConfig(
                    backend="langchain",
                    provider="openai",
                    model="o4-mini",
                    api_key_env="TEST_KEY",
                ),
                messages=[pb.ConversationMessage(role="user", content="Hi")],
            )

            responses = []
            async for resp in provider.generate(request):
                responses.append(resp)

            assert len(responses) == 1
            assert responses[0].HasField("error")
            assert responses[0].error.code == "provider_error"
            assert "Something unexpected" in responses[0].error.message
            assert responses[0].is_final

    @staticmethod
    @staticmethod
    def _http_exc(*, status_code=None, code=None, status=None, message="not found", headers=None):
        exc = Exception(message)
        if status_code is not None:
            exc.status_code = status_code
        if code is not None:
            exc.code = code
        if status is not None:
            exc.status = status
        if headers is not None:
            exc.response = type("Resp", (), {
                "headers": headers,
                "status_code": status_code,
            })()
        return exc

    @staticmethod
    def _fail_then_ok_model(exc, fail_times=1):
        call_count = {"n": 0}
        chunk = AIMessageChunk(content="Success after retry")
        chunk.usage_metadata = None

        class MockModel:
            def astream(self, messages):
                call_count["n"] += 1
                if call_count["n"] <= fail_times:
                    async def boom(_messages=messages):
                        raise exc
                        yield
                    return boom()

                async def good(_messages=messages):
                    yield chunk
                return good()

        return MockModel(), call_count

    @staticmethod
    def _openai_request(**kwargs):
        return pb.GenerateRequest(
            session_id="sess-1",
            execution_id="exec-1",
            llm_config=pb.LLMConfig(
                backend="langchain",
                provider="openai",
                model="o4-mini",
                api_key_env="TEST_KEY",
            ),
            messages=[pb.ConversationMessage(role="user", content="Hi")],
            **kwargs,
        )

    @pytest.mark.asyncio
    @patch.dict(os.environ, {"TEST_KEY": "test-value"})
    @patch("asyncio.sleep", new_callable=AsyncMock)
    @pytest.mark.parametrize(
        "exc_kwargs",
        [
            {"status_code": 404},
            {"code": 404, "status": "NOT_FOUND"},
            {"message": "Error code: 404 - [{'error': {'message': 'Publisher model not found'}}]"},
        ],
        ids=["status_code", "code_attr", "message_only"],
    )
    async def test_generate_retries_on_404_then_succeeds(self, mock_sleep, exc_kwargs, provider):
        model, call_count = self._fail_then_ok_model(self._http_exc(**exc_kwargs))
        with patch.object(provider, "_get_or_create_model", return_value=model):
            responses = []
            async for resp in provider.generate(self._openai_request()):
                responses.append(resp)

        assert call_count["n"] == 2
        assert mock_sleep.call_count == 1
        assert 0 <= mock_sleep.call_args[0][0] <= 1
        text_responses = [r for r in responses if r.HasField("text")]
        assert len(text_responses) == 1
        assert text_responses[0].text.content == "Success after retry"

    @pytest.mark.asyncio
    @patch.dict(os.environ, {"TEST_KEY": "test-value"})
    @patch("asyncio.sleep", new_callable=AsyncMock)
    async def test_generate_retries_on_429_then_succeeds(self, mock_sleep, provider):
        model, call_count = self._fail_then_ok_model(self._http_exc(status_code=429, message="rate limited"))
        with patch.object(provider, "_get_or_create_model", return_value=model):
            responses = []
            async for resp in provider.generate(self._openai_request()):
                responses.append(resp)

        assert call_count["n"] == 2
        text_responses = [r for r in responses if r.HasField("text")]
        assert text_responses[0].text.content == "Success after retry"

    @pytest.mark.asyncio
    @patch.dict(os.environ, {"TEST_KEY": "test-value"})
    @patch("asyncio.sleep", new_callable=AsyncMock)
    async def test_generate_exhausted_404_yields_max_retries(self, mock_sleep, provider):
        model, call_count = self._fail_then_ok_model(
            self._http_exc(status_code=404), fail_times=3,
        )
        with patch.object(provider, "_get_or_create_model", return_value=model):
            responses = []
            async for resp in provider.generate(self._openai_request()):
                responses.append(resp)

        assert call_count["n"] == 3
        assert mock_sleep.call_count == 2
        assert len(responses) == 1
        assert responses[0].HasField("error")
        assert responses[0].error.code == "max_retries"
        assert responses[0].is_final

    @pytest.mark.asyncio
    @patch.dict(os.environ, {"TEST_KEY": "test-value"})
    @pytest.mark.parametrize("status", [401, 403])
    async def test_generate_401_403_does_not_retry(self, status, provider):
        model, call_count = self._fail_then_ok_model(self._http_exc(status_code=status))
        with patch.object(provider, "_get_or_create_model", return_value=model):
            responses = []
            async for resp in provider.generate(self._openai_request()):
                responses.append(resp)

        assert call_count["n"] == 1
        assert responses[0].HasField("error")
        assert responses[0].error.code == "provider_error"
        assert responses[0].is_final

    @pytest.mark.asyncio
    @patch.dict(os.environ, {"TEST_KEY": "test-value"})
    async def test_generate_no_retry_on_404_after_partial_output(self, provider):
        call_count = {"n": 0}
        exc = self._http_exc(status_code=404)

        class MockModel:
            def astream(self, messages):
                call_count["n"] += 1

                async def gen():
                    yield AIMessageChunk(content="Partial data")
                    raise exc
                return gen()

        with patch.object(provider, "_get_or_create_model", return_value=MockModel()):
            responses = []
            async for resp in provider.generate(self._openai_request()):
                responses.append(resp)

        assert call_count["n"] == 1
        assert responses[0].HasField("text")
        assert responses[0].text.content == "Partial data"
        assert responses[1].HasField("error")
        assert responses[1].error.code == "partial_stream_error"
        assert responses[1].is_final

    @pytest.mark.asyncio
    @patch.dict(os.environ, {"TEST_KEY": "test-value"})
    @patch("asyncio.sleep", new_callable=AsyncMock)
    async def test_404_does_not_degrade_cache_markers(self, mock_sleep, provider):
        kinds = []
        attempt = {"n": 0}
        exc = self._http_exc(status_code=404, message="Publisher model not found")

        def fake_get(config, tools, cache_kind=prompt_cache.NONE, strip_ttl=False, execution_id="", disable_tool_calls=False):
            kinds.append((cache_kind, strip_ttl))

            class Model:
                def astream(self, messages):
                    async def gen():
                        attempt["n"] += 1
                        if attempt["n"] == 1:
                            raise exc
                        chunk = AIMessageChunk(content="ok after 404")
                        chunk.usage_metadata = None
                        yield chunk
                    return gen()
            return Model()

        with patch.object(provider, "_get_or_create_model", side_effect=fake_get):
            request = pb.GenerateRequest(
                session_id="sess-1",
                execution_id="exec-1",
                prompt_cache=True,
                llm_config=pb.LLMConfig(
                    provider="anthropic", model="claude-sonnet-4-5", api_key_env="TEST_KEY",
                ),
                messages=[pb.ConversationMessage(role="user", content="hi")],
            )
            texts = []
            async for resp in provider.generate(request):
                if resp.HasField("text"):
                    texts.append(resp.text.content)

        assert kinds == [(prompt_cache.ANTHROPIC, False)]
        assert attempt["n"] == 2
        assert texts == ["ok after 404"]

    @pytest.mark.asyncio
    @patch.dict(os.environ, {"TEST_KEY": "test-value"})
    @patch("asyncio.sleep", new_callable=AsyncMock)
    async def test_generate_honors_retry_after(self, mock_sleep, provider):
        model, call_count = self._fail_then_ok_model(
            self._http_exc(status_code=429, headers={"Retry-After": "53"}),
        )
        with patch.object(provider, "_get_or_create_model", return_value=model):
            responses = []
            async for resp in provider.generate(self._openai_request()):
                responses.append(resp)

        assert call_count["n"] == 2
        mock_sleep.assert_called_once_with(53.0)
        assert any(r.HasField("text") for r in responses)

    @pytest.mark.asyncio
    @patch.dict(os.environ, {"TEST_KEY": "test-value"})
    async def test_generate_spend_cap_429_does_not_retry(self, provider):
        model, call_count = self._fail_then_ok_model(
            self._http_exc(
                status_code=429,
                message="usage_limit_reached",
                headers={"x-should-retry": "false"},
            ),
        )
        with patch.object(provider, "_get_or_create_model", return_value=model):
            responses = []
            async for resp in provider.generate(self._openai_request()):
                responses.append(resp)

        assert call_count["n"] == 1
        assert responses[0].error.code == "provider_error"

    @pytest.mark.asyncio
    @patch.dict(os.environ, {"TEST_KEY": "test-value"})
    async def test_generate_retry_after_over_cap_does_not_retry(self, provider):
        model, call_count = self._fail_then_ok_model(
            self._http_exc(status_code=429, headers={"Retry-After": "3600"}),
        )
        with patch.object(provider, "_get_or_create_model", return_value=model):
            responses = []
            async for resp in provider.generate(self._openai_request()):
                responses.append(resp)

        assert call_count["n"] == 1
        assert responses[0].error.code == "provider_error"

    @pytest.mark.asyncio
    @patch.dict(os.environ, {"TEST_KEY": "test-value"})
    @patch("asyncio.sleep", new_callable=AsyncMock)
    async def test_generate_retries_on_connection_error(self, mock_sleep, provider):
        model, call_count = self._fail_then_ok_model(httpx.ConnectError("connection failed"))
        with patch.object(provider, "_get_or_create_model", return_value=model):
            responses = []
            async for resp in provider.generate(self._openai_request()):
                responses.append(resp)

        assert call_count["n"] == 2
        assert mock_sleep.call_count == 1
        assert any(r.HasField("text") for r in responses)

    @pytest.mark.asyncio
    @patch.dict(os.environ, {"TEST_KEY": "test-value"})
    async def test_generate_cancel_during_retry_sleep(self, provider):
        started = asyncio.Event()
        model, call_count = self._fail_then_ok_model(
            self._http_exc(status_code=429, headers={"Retry-After": "53"}),
        )

        async def block_sleep(*_args, **_kwargs):
            started.set()
            await asyncio.Event().wait()

        with (
            patch.object(provider, "_get_or_create_model", return_value=model),
            patch("llm.providers.retry.asyncio.sleep", side_effect=block_sleep),
        ):
            async def consume():
                responses = []
                async for resp in provider.generate(self._openai_request()):
                    responses.append(resp)
                return responses

            task = asyncio.create_task(consume())
            await asyncio.wait_for(started.wait(), timeout=2)
            task.cancel()
            with pytest.raises(asyncio.CancelledError):
                await task

        assert call_count["n"] == 1


def _looping_tool_history():
    return [
        pb.ConversationMessage(role="system", content="You are an SRE agent."),
        pb.ConversationMessage(role="user", content="Check pods."),
        pb.ConversationMessage(
            role="assistant",
            content="",
            tool_calls=[pb.ToolCall(id="tc1", name="k8s.get_pods", arguments="{}")],
        ),
        pb.ConversationMessage(
            role="tool",
            tool_call_id="tc1",
            tool_name="k8s.get_pods",
            content="pod-1 Running",
        ),
    ]


def _sample_tools():
    return [
        pb.ToolDefinition(
            name="k8s.get_pods",
            description="List pods",
            parameters_schema='{"type": "object"}',
        ),
        pb.ToolDefinition(
            name="k8s.get_logs",
            description="Get logs",
            parameters_schema='{"type": "object"}',
        ),
    ]


def _has_openai_breakpoint(msg) -> bool:
    extra = getattr(msg, "additional_kwargs", None) or {}
    if extra.get("prompt_cache_breakpoint") == {"mode": "explicit"}:
        return True
    content = getattr(msg, "content", None)
    if isinstance(content, list) and content and isinstance(content[0], dict):
        return content[0].get("prompt_cache_breakpoint") == {"mode": "explicit"}
    return False


def _has_anthropic_cache(msg) -> bool:
    extra = getattr(msg, "additional_kwargs", None) or {}
    if extra.get("cache_control") == {"type": "ephemeral", "ttl": "1h"}:
        return True
    content = getattr(msg, "content", None)
    if isinstance(content, list) and content and isinstance(content[0], dict):
        return content[0].get("cache_control") == {"type": "ephemeral", "ttl": "1h"}
    return False


class TestLangChainPromptCacheBreakpoints:
    def test_anthropic_tools_and_messages(self, provider):
        mock_model = MagicMock()
        mock_model.bind_tools.return_value = mock_model
        tools = _sample_tools()
        LangChainProvider._bind_tools(
            mock_model, tools, cache_kind=prompt_cache.ANTHROPIC,
        )
        bound = mock_model.bind_tools.call_args[0][0]
        assert bound[0]["name"] == "k8s__get_pods"
        assert "input_schema" in bound[0]
        assert "cache_control" not in bound[0]
        assert bound[-1]["cache_control"] == {"type": "ephemeral", "ttl": "1h"}

        converted = provider._convert_messages(
            _looping_tool_history(), prompt_cache.ANTHROPIC,
        )
        assert _has_anthropic_cache(converted[0])  # system
        assert _has_anthropic_cache(converted[1])  # first user
        assert not _has_anthropic_cache(converted[2])  # assistant
        last = converted[-1]
        assert isinstance(last, ToolMessage)
        assert _has_anthropic_cache(last)
        result = last.content[0]
        assert result["type"] == "tool_result"
        assert result["tool_use_id"] == "tc1"
        assert result["content"] == "pod-1 Running"
        assert result["cache_control"]["ttl"] == "1h"

    def test_anthropic_sticky_breakpoints_not_assistant(self, provider):
        converted = provider._convert_messages(
            _looping_tool_history(), prompt_cache.ANTHROPIC,
        )
        assert _has_anthropic_cache(converted[0])
        assert _has_anthropic_cache(converted[1])
        assert not _has_anthropic_cache(converted[2])
        assert _has_anthropic_cache(converted[3])
        assert isinstance(converted[3], ToolMessage)

        turn1 = provider._convert_messages(
            _looping_tool_history()[:-1], prompt_cache.ANTHROPIC,
        )
        assert _has_anthropic_cache(turn1[0])
        assert _has_anthropic_cache(turn1[1])
        assert not _has_anthropic_cache(turn1[2])

    def test_anthropic_forced_conclusion_marks_last_tool_not_user(self, provider):
        history = _looping_tool_history() + [
            pb.ConversationMessage(role="user", content="Conclude now."),
        ]
        converted = provider._convert_messages(history, prompt_cache.ANTHROPIC)
        assert _has_anthropic_cache(converted[0])
        assert _has_anthropic_cache(converted[1])
        assert not _has_anthropic_cache(converted[2])
        assert _has_anthropic_cache(converted[3])
        last_user = converted[-1]
        assert isinstance(last_user, HumanMessage)
        assert isinstance(last_user.content, str)
        assert not _has_anthropic_cache(last_user)

    def test_vertex_tool_result_cache_control_not_nested(self, provider):
        from langchain_google_vertexai._anthropic_utils import _format_messages_anthropic

        converted = provider._convert_messages(
            _looping_tool_history(), prompt_cache.ANTHROPIC,
        )
        system, formatted = _format_messages_anthropic(converted, project=None)
        assert system[0]["cache_control"]["ttl"] == "1h"
        first_user = formatted[0]["content"][0]
        assert first_user["type"] == "text"
        assert first_user["cache_control"]["ttl"] == "1h"
        tool_results = [
            block
            for msg in formatted
            for block in (msg["content"] if isinstance(msg["content"], list) else [])
            if isinstance(block, dict) and block.get("type") == "tool_result"
        ]
        assert tool_results
        for block in tool_results:
            assert block["cache_control"]["ttl"] == "1h"
            inner = block["content"]
            assert isinstance(inner, str) or isinstance(inner, list)
            if isinstance(inner, list):
                for part in inner:
                    if isinstance(part, dict):
                        assert "cache_control" not in part

    def test_anthropic_skips_tool_breakpoint_when_no_tools(self, provider):
        mock_model = MagicMock()
        result = LangChainProvider._bind_tools(
            mock_model, [], cache_kind=prompt_cache.ANTHROPIC,
        )
        mock_model.bind_tools.assert_not_called()
        assert result is mock_model

    def test_no_markers_when_cache_off(self, provider):
        messages = provider._convert_messages(_looping_tool_history())
        assert isinstance(messages[0].content, str)
        assert messages[-1].additional_kwargs.get("cache_control") in (None, {})

        mock_model = MagicMock()
        mock_model.bind_tools.return_value = mock_model
        LangChainProvider._bind_tools(mock_model, _sample_tools())
        bound = mock_model.bind_tools.call_args[0][0]
        assert bound[0]["type"] == "function"
        assert "cache_control" not in bound[-1]
        assert "prompt_cache_breakpoint" not in bound[-1]

    def test_openai_sticky_breakpoints_not_assistant(self, provider):
        converted = provider._convert_messages(
            _looping_tool_history(), prompt_cache.OPENAI_EXPLICIT,
        )
        assert _has_openai_breakpoint(converted[0])  # system
        assert _has_openai_breakpoint(converted[1])  # first user
        assert not _has_openai_breakpoint(converted[2])  # assistant
        assert _has_openai_breakpoint(converted[3])  # last tool
        assert isinstance(converted[3], ToolMessage)

        mock_model = MagicMock()
        mock_model.bind_tools.return_value = mock_model
        LangChainProvider._bind_tools(
            mock_model, _sample_tools(), cache_kind=prompt_cache.OPENAI_EXPLICIT,
        )
        bound = mock_model.bind_tools.call_args[0][0]
        assert bound[-1]["prompt_cache_breakpoint"] == {"mode": "explicit"}
        assert "prompt_cache_breakpoint" not in bound[0]

    def test_openai_forced_conclusion_user_unmarked(self, provider):
        history = _looping_tool_history() + [
            pb.ConversationMessage(role="user", content="Conclude now."),
        ]
        converted = provider._convert_messages(history, prompt_cache.OPENAI_EXPLICIT)
        assert _has_openai_breakpoint(converted[0])
        assert _has_openai_breakpoint(converted[1])
        assert not _has_openai_breakpoint(converted[2])
        assert _has_openai_breakpoint(converted[3])
        assert not _has_openai_breakpoint(converted[4])

    @patch.dict(os.environ, {"OPENAI_API_KEY": "test-key"})
    def test_openai_options_not_on_constructor(self, provider):
        captured = {}

        class FakeChatOpenAI:
            def __init__(self, **kwargs):
                captured["ctor"] = kwargs

            def bind_tools(self, tools, **kwargs):
                captured["tools"] = tools
                return self

            def bind(self, **kwargs):
                captured["bind"] = kwargs
                return self

        with patch("langchain_openai.ChatOpenAI", FakeChatOpenAI):
            config = pb.LLMConfig(
                provider="openai", model="gpt-5.6", api_key_env="OPENAI_API_KEY",
            )
            provider._get_or_create_model(
                config, _sample_tools(), prompt_cache.OPENAI_EXPLICIT, False, "exec-99",
            )

        assert "prompt_cache_options" not in captured["ctor"]
        assert "prompt_cache_key" not in captured["ctor"]
        assert captured["ctor"]["max_retries"] == 0
        assert captured["bind"]["prompt_cache_key"] == "exec-99"
        assert captured["bind"]["prompt_cache_options"] == {
            "mode": "explicit", "ttl": "30m",
        }
        assert "max_retries" not in captured["bind"]

    @patch.dict(os.environ, {"OPENAI_API_KEY": "test-key"})
    def test_openai_disable_binds_options_without_breakpoints(self, provider):
        captured = {}

        class FakeChatOpenAI:
            def __init__(self, **kwargs):
                captured["ctor"] = kwargs

            def bind_tools(self, tools, **kwargs):
                captured["tools"] = tools
                captured["bind_tools_kwargs"] = kwargs
                return self

            def bind(self, **kwargs):
                captured["bind"] = kwargs
                return self

        with patch("langchain_openai.ChatOpenAI", FakeChatOpenAI):
            config = pb.LLMConfig(
                provider="openai", model="gpt-5.6", api_key_env="OPENAI_API_KEY",
            )
            kind = prompt_cache.classify_cache(config, False, "exec-99")
            assert kind == prompt_cache.OPENAI_EXPLICIT_DISABLE
            provider._get_or_create_model(
                config, _sample_tools(), kind, False, "exec-99",
            )

        assert "prompt_cache_options" not in captured["ctor"]
        assert captured["bind"]["prompt_cache_key"] == "exec-99"
        assert captured["bind"]["prompt_cache_options"] == {
            "mode": "explicit", "ttl": "30m",
        }
        assert "prompt_cache_breakpoint" not in captured["tools"][-1]
        converted = provider._convert_messages(
            _looping_tool_history(), prompt_cache.OPENAI_EXPLICIT_DISABLE,
        )
        assert isinstance(converted[0].content, str)
        assert not any(_has_openai_breakpoint(m) for m in converted)

    @patch.dict(os.environ, {"OPENAI_API_KEY": "test-key"})
    def test_disable_tool_calls_sets_tool_choice_none(self, provider):
        captured = {}

        class FakeChatOpenAI:
            def __init__(self, **kwargs):
                captured["ctor"] = kwargs

            def bind_tools(self, tools, **kwargs):
                captured["tools"] = tools
                captured["bind_tools_kwargs"] = kwargs
                return self

            def bind(self, **kwargs):
                captured["bind"] = kwargs
                return self

        with patch("langchain_openai.ChatOpenAI", FakeChatOpenAI):
            config = pb.LLMConfig(
                provider="openai", model="gpt-5.6", api_key_env="OPENAI_API_KEY",
            )
            provider._get_or_create_model(
                config, _sample_tools(), prompt_cache.OPENAI_EXPLICIT, False, "exec-99",
                disable_tool_calls=True,
            )

        assert captured["bind_tools_kwargs"]["tool_choice"] == "none"

    @patch.dict(os.environ, {"ANTHROPIC_API_KEY": "test-key"})
    def test_anthropic_max_retries_on_constructor_not_bind(self, provider):
        captured = {}

        class FakeChatAnthropic:
            def __init__(self, **kwargs):
                captured["ctor"] = kwargs

            def bind_tools(self, tools, **kwargs):
                captured["tools"] = tools
                return self

            def bind(self, **kwargs):
                captured["bind"] = kwargs
                return self

        with patch("langchain_anthropic.ChatAnthropic", FakeChatAnthropic):
            config = pb.LLMConfig(
                provider="anthropic", model="claude-sonnet-4-5",
                api_key_env="ANTHROPIC_API_KEY",
            )
            provider._get_or_create_model(
                config, _sample_tools(), prompt_cache.ANTHROPIC, False, "exec-1",
            )

        assert captured["ctor"]["max_retries"] == 0
        assert "bind" not in captured

    def test_vertex_claude_max_retries_on_constructor_not_bind(self, provider):
        captured = {}

        class FakeChatAnthropicVertex:
            def __init__(self, **kwargs):
                captured["ctor"] = kwargs

            def bind_tools(self, tools, **kwargs):
                captured["tools"] = tools
                return self

            def bind(self, **kwargs):
                captured["bind"] = kwargs
                return self

        with patch(
            "langchain_google_vertexai.model_garden.ChatAnthropicVertex",
            FakeChatAnthropicVertex,
        ):
            config = pb.LLMConfig(
                provider="vertexai",
                model="claude-sonnet-4-5",
                project="p",
                location="us-east5",
            )
            provider._get_or_create_model(
                config, _sample_tools(), prompt_cache.ANTHROPIC, False, "exec-1",
            )

        assert captured["ctor"]["max_retries"] == 0
        assert "bind" not in captured

    @patch.dict(os.environ, {"XAI_API_KEY": "test-key"})
    def test_xai_max_retries_on_constructor(self, provider):
        captured = {}

        class FakeChatXAI:
            def __init__(self, **kwargs):
                captured["ctor"] = kwargs

            def bind_tools(self, tools, **kwargs):
                return self

        with patch("langchain_xai.ChatXAI", FakeChatXAI):
            config = pb.LLMConfig(
                provider="xai", model="grok-4", api_key_env="XAI_API_KEY",
            )
            provider._get_or_create_model(
                config, _sample_tools(), prompt_cache.NONE, False, "exec-1",
            )

        assert captured["ctor"]["max_retries"] == 0

    @patch.dict(os.environ, {"GOOGLE_API_KEY": "test-key"})
    def test_google_max_retries_on_constructor(self, provider):
        captured = {}

        class FakeChatGoogle:
            def __init__(self, **kwargs):
                captured["ctor"] = kwargs

            def bind_tools(self, tools, **kwargs):
                return self

        with patch("langchain_google_genai.ChatGoogleGenerativeAI", FakeChatGoogle):
            config = pb.LLMConfig(
                provider="google", model="gemini-2.5-pro", api_key_env="GOOGLE_API_KEY",
            )
            provider._get_or_create_model(
                config, _sample_tools(), prompt_cache.NONE, False, "exec-1",
            )

        assert captured["ctor"]["max_retries"] == 0

    def test_vertex_gemini_max_retries_on_constructor(self, provider):
        captured = {}

        class FakeChatGoogle:
            def __init__(self, **kwargs):
                captured["ctor"] = kwargs

            def bind_tools(self, tools, **kwargs):
                return self

        with patch("langchain_google_genai.ChatGoogleGenerativeAI", FakeChatGoogle):
            config = pb.LLMConfig(
                provider="vertexai",
                model="gemini-2.5-pro",
                project="p",
                location="us-central1",
            )
            provider._get_or_create_model(
                config, _sample_tools(), prompt_cache.NONE, False, "exec-1",
            )

        assert captured["ctor"]["max_retries"] == 0

    @pytest.mark.parametrize("model", ["gpt-5.5", "gpt-5.2", "gpt-5", "gpt-5-mini"])
    @patch.dict(os.environ, {"OPENAI_API_KEY": "test-key"})
    def test_older_openai_does_not_bind_cache_options(self, provider, model):
        captured = {}

        class FakeChatOpenAI:
            def __init__(self, **kwargs):
                captured["ctor"] = kwargs

            def bind_tools(self, tools, **kwargs):
                captured["tools"] = tools
                return self

            def bind(self, **kwargs):
                captured["bind"] = kwargs
                return self

        with patch("langchain_openai.ChatOpenAI", FakeChatOpenAI):
            config = pb.LLMConfig(
                provider="openai", model=model, api_key_env="OPENAI_API_KEY",
            )
            kind = prompt_cache.classify_cache(config, True, "exec-1")
            assert kind == prompt_cache.NONE
            provider._get_or_create_model(config, _sample_tools(), kind, False, "exec-1")

        assert "bind" not in captured
        assert "prompt_cache_breakpoint" not in captured["tools"][-1]

    @pytest.mark.asyncio
    async def test_openai_two_turn_create_then_read(self, provider):
        histories = [
            _looping_tool_history()[:-1],  # turn 1 ends at assistant tool call
            _looping_tool_history(),  # turn 2 ends in tool result
        ]
        usages = [
            {"input_tokens": 1000, "output_tokens": 20, "total_tokens": 1020,
             "input_token_details": {"cache_write_tokens": 800, "cached_tokens": 0}},
            {"input_tokens": 1100, "output_tokens": 30, "total_tokens": 1130,
             "input_token_details": {"cached_tokens": 800, "cache_write_tokens": 0}},
        ]
        call = {"n": 0}

        class MockModel:
            def astream(self, messages):
                idx = call["n"]
                call["n"] += 1
                last = messages[-1]
                if idx == 0:
                    assert not _has_openai_breakpoint(last)
                    assert _has_openai_breakpoint(messages[0])
                    assert _has_openai_breakpoint(messages[1])
                if idx == 1:
                    assert isinstance(last, ToolMessage)
                    assert _has_openai_breakpoint(last)
                    assert not _has_openai_breakpoint(messages[2])

                async def gen():
                    chunk = AIMessageChunk(content="ok")
                    chunk.usage_metadata = usages[idx]
                    yield chunk
                return gen()

        with patch.object(provider, "_get_or_create_model", return_value=MockModel()):
            results = []
            for history in histories:
                request = pb.GenerateRequest(
                    session_id="sess-1",
                    execution_id="exec-1",
                    prompt_cache=True,
                    llm_config=pb.LLMConfig(
                        provider="openai", model="gpt-5.6", api_key_env="TEST_KEY",
                    ),
                    messages=history,
                    tools=_sample_tools(),
                )
                usage = None
                async for resp in provider.generate(request):
                    if resp.HasField("usage"):
                        usage = resp.usage
                results.append(usage)

        assert results[0].cache_creation_tokens == 800
        assert results[1].cache_read_tokens == 800

    @pytest.mark.asyncio
    async def test_400_strip_ttl_then_all(self, provider):
        class Fake400(Exception):
            status_code = 400

        kinds = []
        attempt = {"n": 0}

        def fake_get(config, tools, cache_kind=prompt_cache.NONE, strip_ttl=False, execution_id="", disable_tool_calls=False):
            kinds.append((cache_kind, strip_ttl))

            class Model:
                def astream(self, messages):
                    async def gen():
                        attempt["n"] += 1
                        if attempt["n"] < 3:
                            raise Fake400("extra inputs are not permitted")
                        chunk = AIMessageChunk(content="ok after strip")
                        chunk.usage_metadata = None
                        yield chunk
                    return gen()
            return Model()

        with patch.object(provider, "_get_or_create_model", side_effect=fake_get):
            request = pb.GenerateRequest(
                session_id="sess-1",
                execution_id="exec-1",
                prompt_cache=True,
                llm_config=pb.LLMConfig(
                    provider="anthropic", model="claude-sonnet-4-5", api_key_env="TEST_KEY",
                ),
                messages=[pb.ConversationMessage(role="user", content="hi")],
            )
            texts = []
            async for resp in provider.generate(request):
                if resp.HasField("text"):
                    texts.append(resp.text.content)

        assert kinds == [
            (prompt_cache.ANTHROPIC, False),
            (prompt_cache.ANTHROPIC, True),
            (prompt_cache.NONE, False),
        ]
        assert texts == ["ok after strip"]

