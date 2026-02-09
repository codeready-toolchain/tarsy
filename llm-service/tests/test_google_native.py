"""Tests for GoogleNativeProvider."""
import os
import pytest
from unittest.mock import AsyncMock, MagicMock, Mock, patch
from google.genai import types as genai_types

from proto import llm_service_pb2 as pb
from llm.providers.google_native import GoogleNativeProvider

pytestmark = pytest.mark.unit


@pytest.fixture
def provider():
    """Create a GoogleNativeProvider instance."""
    return GoogleNativeProvider()


@pytest.fixture
def mock_genai_client():
    """Create a mock genai client with async support."""
    client = MagicMock()
    client.aio = MagicMock()
    client.aio.models = MagicMock()
    return client


class TestGoogleNativeProvider:
    """Test GoogleNativeProvider functionality."""

    def test_tool_name_conversion_to_native(self):
        """Test conversion from server.tool to server__tool format."""
        assert GoogleNativeProvider._tool_name_to_native("server.tool") == "server__tool"
        assert GoogleNativeProvider._tool_name_to_native("my.server.tool") == "my__server__tool"
        assert GoogleNativeProvider._tool_name_to_native("notool") == "notool"

    def test_tool_name_to_native_rejects_double_underscore(self):
        """Test that tool names with __ in segments are rejected."""
        with pytest.raises(ValueError, match="contains '__'"):
            GoogleNativeProvider._tool_name_to_native("server.my__helper")
        with pytest.raises(ValueError, match="contains '__'"):
            GoogleNativeProvider._tool_name_to_native("my__server.tool")

    def test_tool_name_conversion_from_native(self):
        """Test conversion from server__tool back to server.tool format."""
        assert GoogleNativeProvider._tool_name_from_native("server__tool") == "server.tool"
        assert GoogleNativeProvider._tool_name_from_native("my__server__tool") == "my.server.tool"
        assert GoogleNativeProvider._tool_name_from_native("notool") == "notool"

    @patch.dict(os.environ, {"TEST_API_KEY": "test-key-123"})
    @patch("llm.providers.google_native.genai.Client")
    def test_get_client_creates_client(self, mock_client_class, provider):
        """Test that _get_client creates a client when not cached."""
        mock_instance = Mock()
        mock_client_class.return_value = mock_instance
        
        result = provider._get_client("TEST_API_KEY")
        
        assert result is mock_instance
        mock_client_class.assert_called_once_with(api_key="test-key-123")

    @patch.dict(os.environ, {"TEST_API_KEY": "test-key-123"})
    @patch("llm.providers.google_native.genai.Client")
    def test_get_client_caches_client(self, mock_client_class, provider):
        """Test that _get_client returns cached client on subsequent calls."""
        mock_instance = Mock()
        mock_client_class.return_value = mock_instance
        
        result1 = provider._get_client("TEST_API_KEY")
        result2 = provider._get_client("TEST_API_KEY")
        
        assert result1 is result2
        mock_client_class.assert_called_once()

    @patch.dict(os.environ, {}, clear=True)
    def test_get_client_raises_on_missing_env_var(self, provider):
        """Test that _get_client raises ValueError when API key env var is not set."""
        with pytest.raises(ValueError, match="Environment variable 'MISSING_KEY' is not set"):
            provider._get_client("MISSING_KEY")

    def test_get_thinking_config_gemini_2_5_pro(self, provider):
        """Test thinking config for Gemini 2.5 Pro models."""
        config = provider._get_thinking_config("gemini-2.5-pro-latest")
        
        assert config.thinking_budget == 32768
        assert config.include_thoughts is True

    def test_get_thinking_config_gemini_2_5_flash(self, provider):
        """Test thinking config for Gemini 2.5 Flash models."""
        config = provider._get_thinking_config("gemini-2.5-flash")
        
        assert config.thinking_budget == 24576
        assert config.include_thoughts is True

    def test_get_thinking_config_default(self, provider):
        """Test thinking config for other models uses default."""
        config = provider._get_thinking_config("gemini-3.0")
        
        assert config.thinking_level == genai_types.ThinkingLevel.HIGH
        assert config.include_thoughts is True

    def test_convert_messages_system_instruction(self, provider):
        """Test that system messages are extracted as system_instruction."""
        messages = [
            pb.ConversationMessage(role="system", content="You are a helpful assistant"),
            pb.ConversationMessage(role="user", content="Hello"),
        ]
        
        system_instruction, contents = provider._convert_messages(messages)
        
        assert system_instruction == "You are a helpful assistant"
        assert len(contents) == 1
        assert contents[0].role == "user"

    def test_convert_messages_multiple_system_raises(self, provider):
        """Test that multiple system messages raise ValueError."""
        messages = [
            pb.ConversationMessage(role="system", content="First system message"),
            pb.ConversationMessage(role="user", content="Hello"),
            pb.ConversationMessage(role="system", content="Second system message"),
        ]

        with pytest.raises(ValueError, match="Multiple system messages provided \\(duplicate at index 2\\)"):
            provider._convert_messages(messages)

    def test_convert_messages_user_and_assistant(self, provider):
        """Test conversion of user and assistant messages."""
        messages = [
            pb.ConversationMessage(role="user", content="Hello"),
            pb.ConversationMessage(role="assistant", content="Hi there"),
        ]
        
        _, contents = provider._convert_messages(messages)
        
        assert len(contents) == 2
        assert contents[0].role == "user"
        assert contents[0].parts[0].text == "Hello"
        assert contents[1].role == "model"
        assert contents[1].parts[0].text == "Hi there"

    def test_convert_messages_with_tool_calls(self, provider):
        """Test conversion of assistant messages with tool calls."""
        tool_call = pb.ToolCall(
            id="123",
            name="server.tool",
            arguments='{"arg": "value"}',
        )
        messages = [
            pb.ConversationMessage(
                role="assistant",
                content="Let me call a tool",
                tool_calls=[tool_call],
            ),
        ]
        
        _, contents = provider._convert_messages(messages)
        
        assert len(contents) == 1
        assert contents[0].role == "model"
        assert len(contents[0].parts) == 2
        assert contents[0].parts[0].text == "Let me call a tool"
        assert contents[0].parts[1].function_call.name == "server__tool"
        assert contents[0].parts[1].function_call.args["arg"] == "value"

    def test_convert_messages_tool_result(self, provider):
        """Test conversion of tool result messages."""
        messages = [
            pb.ConversationMessage(
                role="tool",
                tool_name="server.tool",
                content='{"result": "success"}',
            ),
        ]
        
        _, contents = provider._convert_messages(messages)
        
        assert len(contents) == 1
        assert contents[0].role == "user"
        assert contents[0].parts[0].function_response.name == "server__tool"
        assert contents[0].parts[0].function_response.response["result"] == "success"

    def test_convert_tools_with_mcp_tools(self, provider):
        """Test conversion of MCP tools to function declarations."""
        tools = [
            pb.ToolDefinition(
                name="server.read",
                description="Read a file",
                parameters_schema='{"type": "object", "properties": {"path": {"type": "string"}}}',
            ),
        ]
        
        result = provider._convert_tools(tools, {})
        
        assert len(result) == 1
        assert len(result[0].function_declarations) == 1
        decl = result[0].function_declarations[0]
        assert decl.name == "server__read"
        assert decl.description == "Read a file"
        assert decl.parameters is not None

    def test_convert_tools_native_tools(self, provider):
        """Test conversion of native tools when no MCP tools present."""
        native_tools = {
            "google_search": True,
            "code_execution": True,
        }
        
        result = provider._convert_tools([], native_tools)
        
        assert len(result) == 2
        assert isinstance(result[0].google_search, genai_types.GoogleSearch)
        assert isinstance(result[1].code_execution, genai_types.ToolCodeExecution)

    def test_convert_tools_mcp_suppresses_native(self, provider):
        """Test that MCP tools suppress native tools."""
        tools = [pb.ToolDefinition(name="server.tool", description="A tool")]
        native_tools = {"google_search": True}
        
        result = provider._convert_tools(tools, native_tools)
        
        assert len(result) == 1
        assert hasattr(result[0], "function_declarations")

    def test_thought_signature_caching(self, provider):
        """Test thought signature caching and retrieval."""
        execution_id = "exec-123"
        signature = "thought-sig-abc"
        
        provider._cache_thought_signature(execution_id, signature)
        cached = provider._get_cached_thought_signature(execution_id)
        
        assert cached == signature

    def test_thought_signature_cache_miss(self, provider):
        """Test that cache miss returns None."""
        cached = provider._get_cached_thought_signature("nonexistent")
        assert cached is None

    @pytest.mark.asyncio
    @patch.dict(os.environ, {"TEST_API_KEY": "test-key-123"})
    @patch("llm.providers.google_native.genai.Client")
    async def test_generate_missing_api_key(self, mock_client_class, provider):
        """Test that generate yields error when API key env var is missing."""
        with patch.dict(os.environ, {}, clear=True):
            request = pb.GenerateRequest(
                session_id="sess-1",
                execution_id="exec-1",
                llm_config=pb.LLMConfig(
                    backend="google-native",
                    model="gemini-2.5-pro",
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
    @patch.dict(os.environ, {"TEST_API_KEY": "test-key-123"})
    @patch("llm.providers.google_native.genai.Client")
    async def test_generate_success(self, mock_client_class, provider):
        """Test successful generation with text response."""
        mock_client = MagicMock()
        mock_client_class.return_value = mock_client
        
        mock_part = MagicMock()
        mock_part.thought = False
        mock_part.text = "Hello, world!"
        mock_part.function_call = None
        mock_part.executable_code = None
        mock_part.code_execution_result = None
        
        mock_candidate = MagicMock()
        mock_candidate.content = MagicMock()
        mock_candidate.content.parts = [mock_part]
        mock_candidate.grounding_metadata = None
        
        mock_chunk = MagicMock()
        mock_chunk.candidates = [mock_candidate]
        mock_chunk.usage_metadata = None
        
        async def mock_stream():
            yield mock_chunk
        
        mock_client.aio.models.generate_content_stream = AsyncMock(return_value=mock_stream())
        
        request = pb.GenerateRequest(
            session_id="sess-1",
            execution_id="exec-1",
            llm_config=pb.LLMConfig(
                backend="google-native",
                model="gemini-2.5-pro",
                api_key_env="TEST_API_KEY",
            ),
            messages=[pb.ConversationMessage(role="user", content="Hi")],
        )
        
        responses = []
        async for resp in provider.generate(request):
            responses.append(resp)
        
        assert len(responses) == 2
        assert responses[0].HasField("text")
        assert responses[0].text.content == "Hello, world!"
        assert responses[1].is_final

    @pytest.mark.asyncio
    @patch.dict(os.environ, {"TEST_API_KEY": "test-key-123"})
    @patch("llm.providers.google_native.genai.Client")
    async def test_generate_with_usage_info(self, mock_client_class, provider):
        """Test that usage metadata is properly extracted and yielded."""
        mock_client = MagicMock()
        mock_client_class.return_value = mock_client
        
        mock_part = MagicMock()
        mock_part.thought = False
        mock_part.text = "Response"
        mock_part.function_call = None
        mock_part.executable_code = None
        mock_part.code_execution_result = None
        
        mock_candidate = MagicMock()
        mock_candidate.content = MagicMock()
        mock_candidate.content.parts = [mock_part]
        mock_candidate.grounding_metadata = None
        
        mock_usage = MagicMock()
        mock_usage.prompt_token_count = 10
        mock_usage.candidates_token_count = 20
        mock_usage.total_token_count = 30
        mock_usage.thinking_token_count = 5
        
        mock_chunk = MagicMock()
        mock_chunk.candidates = [mock_candidate]
        mock_chunk.usage_metadata = mock_usage
        
        async def mock_stream():
            yield mock_chunk
        
        mock_client.aio.models.generate_content_stream = AsyncMock(return_value=mock_stream())
        
        request = pb.GenerateRequest(
            session_id="sess-1",
            execution_id="exec-1",
            llm_config=pb.LLMConfig(
                backend="google-native",
                model="gemini-2.5-pro",
                api_key_env="TEST_API_KEY",
            ),
            messages=[pb.ConversationMessage(role="user", content="Hi")],
        )
        
        responses = []
        async for resp in provider.generate(request):
            responses.append(resp)
        
        usage_responses = [r for r in responses if r.HasField("usage")]
        assert len(usage_responses) == 1
        usage = usage_responses[0].usage
        assert usage.input_tokens == 10
        assert usage.output_tokens == 20
        assert usage.total_tokens == 30
        assert usage.thinking_tokens == 5

    @pytest.mark.asyncio
    @patch.dict(os.environ, {"TEST_API_KEY": "test-key-123"})
    @patch("llm.providers.google_native.genai.Client")
    @patch("asyncio.sleep", new_callable=AsyncMock)
    async def test_generate_retries_on_empty_stream(self, mock_sleep, mock_client_class, provider):
        """Test that retries happen when zero chunks were produced."""
        mock_client = MagicMock()
        mock_client_class.return_value = mock_client

        call_count = 0

        # First call: empty stream (triggers retry), second call: success
        mock_part = MagicMock()
        mock_part.thought = False
        mock_part.text = "Success after retry"
        mock_part.function_call = None
        mock_part.executable_code = None
        mock_part.code_execution_result = None

        mock_candidate = MagicMock()
        mock_candidate.content = MagicMock()
        mock_candidate.content.parts = [mock_part]
        mock_candidate.grounding_metadata = None

        mock_chunk = MagicMock()
        mock_chunk.candidates = [mock_candidate]
        mock_chunk.usage_metadata = None

        async def empty_stream():
            # Yield nothing — triggers "Empty response" RetryableError
            return
            yield  # make it an async generator

        async def good_stream():
            yield mock_chunk

        async def side_effect(*args, **kwargs):
            nonlocal call_count
            call_count += 1
            if call_count == 1:
                return empty_stream()
            return good_stream()

        mock_client.aio.models.generate_content_stream = AsyncMock(side_effect=side_effect)

        request = pb.GenerateRequest(
            session_id="sess-1",
            execution_id="exec-1",
            llm_config=pb.LLMConfig(
                backend="google-native",
                model="gemini-2.5-pro",
                api_key_env="TEST_API_KEY",
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
    @patch.dict(os.environ, {"TEST_API_KEY": "test-key-123"})
    @patch("llm.providers.google_native.genai.Client")
    async def test_generate_no_retry_after_partial_output(self, mock_client_class, provider):
        """Test that no retry happens when chunks were already yielded.

        We patch _stream_with_timeout to yield one chunk then raise
        _RetryableError, simulating a timeout mid-stream. The generate()
        method should NOT retry because output was already sent.
        """
        from llm.providers.google_native import _RetryableError

        mock_client = MagicMock()
        mock_client_class.return_value = mock_client

        call_count = 0

        async def mock_stream_partial(*args, **kwargs):
            nonlocal call_count
            call_count += 1
            yield pb.GenerateResponse(text=pb.TextDelta(content="Partial data"))
            raise _RetryableError("timeout after partial output")

        with patch.object(provider, "_stream_with_timeout", side_effect=mock_stream_partial):
            request = pb.GenerateRequest(
                session_id="sess-1",
                execution_id="exec-1",
                llm_config=pb.LLMConfig(
                    backend="google-native",
                    model="gemini-2.5-pro",
                    api_key_env="TEST_API_KEY",
                ),
                messages=[pb.ConversationMessage(role="user", content="Hi")],
            )

            responses = []
            async for resp in provider.generate(request):
                responses.append(resp)

        # Should have the partial text chunk + an error (no retry)
        assert call_count == 1  # No retry attempted
        assert len(responses) == 2
        assert responses[0].HasField("text")
        assert responses[0].text.content == "Partial data"
        assert responses[1].HasField("error")
        assert responses[1].error.code == "partial_stream_error"
        assert responses[1].is_final


class TestBuildGroundingDelta:
    """Tests for _build_grounding_delta method."""

    def test_google_search_grounding(self, provider):
        """Test conversion of Google Search grounding metadata."""
        gm = MagicMock()
        gm.web_search_queries = ["UEFA Euro 2024 winner", "Spain Euro 2024"]
        web1 = MagicMock()
        web1.uri = "https://www.uefa.com/euro2024/"
        web1.title = "UEFA.com"
        chunk1 = MagicMock()
        chunk1.web = web1
        gm.grounding_chunks = [chunk1]
        segment = MagicMock()
        segment.start_index = 0
        segment.end_index = 20
        segment.text = "Spain won Euro 2024"
        support = MagicMock()
        support.segment = segment
        support.grounding_chunk_indices = [0]
        gm.grounding_supports = [support]
        gm.search_entry_point = MagicMock()
        gm.search_entry_point.rendered_content = "<div>search widget</div>"

        result = provider._build_grounding_delta(gm)

        assert result.HasField("grounding")
        delta = result.grounding
        assert list(delta.web_search_queries) == ["UEFA Euro 2024 winner", "Spain Euro 2024"]
        assert len(delta.grounding_chunks) == 1
        assert delta.grounding_chunks[0].uri == "https://www.uefa.com/euro2024/"
        assert delta.grounding_chunks[0].title == "UEFA.com"
        assert len(delta.grounding_supports) == 1
        assert delta.grounding_supports[0].start_index == 0
        assert delta.grounding_supports[0].end_index == 20
        assert delta.grounding_supports[0].text == "Spain won Euro 2024"
        assert list(delta.grounding_supports[0].grounding_chunk_indices) == [0]
        assert delta.search_entry_point_html == "<div>search widget</div>"

    def test_url_context_grounding(self, provider):
        """Test conversion of URL Context grounding (no queries)."""
        gm = MagicMock()
        gm.web_search_queries = None
        web1 = MagicMock()
        web1.uri = "https://docs.k8s.io/pods"
        web1.title = "Kubernetes Pods"
        chunk1 = MagicMock()
        chunk1.web = web1
        gm.grounding_chunks = [chunk1]
        gm.grounding_supports = []
        gm.search_entry_point = None

        result = provider._build_grounding_delta(gm)

        delta = result.grounding
        assert len(delta.web_search_queries) == 0
        assert len(delta.grounding_chunks) == 1
        assert delta.grounding_chunks[0].uri == "https://docs.k8s.io/pods"
        assert delta.grounding_chunks[0].title == "Kubernetes Pods"
        assert len(delta.grounding_supports) == 0
        assert delta.search_entry_point_html == ""

    def test_empty_grounding_metadata(self, provider):
        """Test conversion with empty grounding metadata."""
        gm = MagicMock()
        gm.web_search_queries = None
        gm.grounding_chunks = None
        gm.grounding_supports = None
        gm.search_entry_point = None

        result = provider._build_grounding_delta(gm)

        delta = result.grounding
        assert len(delta.web_search_queries) == 0
        assert len(delta.grounding_chunks) == 0
        assert len(delta.grounding_supports) == 0
        assert delta.search_entry_point_html == ""

    def test_partial_grounding_metadata(self, provider):
        """Test conversion with only some fields populated."""
        gm = MagicMock()
        gm.web_search_queries = ["test query"]
        gm.grounding_chunks = None
        gm.grounding_supports = None
        gm.search_entry_point = None

        result = provider._build_grounding_delta(gm)

        delta = result.grounding
        assert list(delta.web_search_queries) == ["test query"]
        assert len(delta.grounding_chunks) == 0
        assert len(delta.grounding_supports) == 0

    def test_grounding_chunk_without_web(self, provider):
        """Test that grounding chunks without web attribute are skipped."""
        gm = MagicMock()
        gm.web_search_queries = None
        chunk1 = MagicMock()
        chunk1.web = None  # No web attribute
        gm.grounding_chunks = [chunk1]
        gm.grounding_supports = None
        gm.search_entry_point = None

        result = provider._build_grounding_delta(gm)

        delta = result.grounding
        assert len(delta.grounding_chunks) == 0

    def test_multiple_sources_and_supports(self, provider):
        """Test conversion with multiple grounding chunks and supports."""
        gm = MagicMock()
        gm.web_search_queries = ["query1"]
        web1 = MagicMock()
        web1.uri = "https://example1.com"
        web1.title = "Example 1"
        web2 = MagicMock()
        web2.uri = "https://example2.com"
        web2.title = "Example 2"
        chunk1 = MagicMock()
        chunk1.web = web1
        chunk2 = MagicMock()
        chunk2.web = web2
        gm.grounding_chunks = [chunk1, chunk2]

        segment1 = MagicMock()
        segment1.start_index = 0
        segment1.end_index = 10
        segment1.text = "First part"
        support1 = MagicMock()
        support1.segment = segment1
        support1.grounding_chunk_indices = [0]

        segment2 = MagicMock()
        segment2.start_index = 11
        segment2.end_index = 20
        segment2.text = "Second part"
        support2 = MagicMock()
        support2.segment = segment2
        support2.grounding_chunk_indices = [0, 1]

        gm.grounding_supports = [support1, support2]
        gm.search_entry_point = None

        result = provider._build_grounding_delta(gm)

        delta = result.grounding
        assert len(delta.grounding_chunks) == 2
        assert len(delta.grounding_supports) == 2
        assert list(delta.grounding_supports[1].grounding_chunk_indices) == [0, 1]

    def test_support_without_segment(self, provider):
        """Test grounding support without segment attribute."""
        gm = MagicMock()
        gm.web_search_queries = None
        gm.grounding_chunks = None
        support = MagicMock(spec=[])  # No attributes at all
        gm.grounding_supports = [support]
        gm.search_entry_point = None

        result = provider._build_grounding_delta(gm)

        delta = result.grounding
        assert len(delta.grounding_supports) == 1
        assert delta.grounding_supports[0].start_index == 0
        assert delta.grounding_supports[0].end_index == 0
        assert delta.grounding_supports[0].text == ""
        assert list(delta.grounding_supports[0].grounding_chunk_indices) == []

    @pytest.mark.asyncio
    @patch.dict(os.environ, {"TEST_API_KEY": "test-key-123"})
    @patch("llm.providers.google_native.genai.Client")
    async def test_grounding_yielded_in_stream(self, mock_client_class, provider):
        """Test that grounding metadata is yielded after content, before usage."""
        mock_client = MagicMock()
        mock_client_class.return_value = mock_client

        # Build a mock chunk with text content and grounding metadata
        mock_part = MagicMock()
        mock_part.thought = False
        mock_part.text = "Spain won Euro 2024"
        mock_part.function_call = None
        mock_part.executable_code = None
        mock_part.code_execution_result = None

        mock_gm = MagicMock()
        mock_gm.web_search_queries = ["Euro 2024 winner"]
        mock_gm.grounding_chunks = []
        mock_gm.grounding_supports = []
        mock_gm.search_entry_point = None

        mock_candidate = MagicMock()
        mock_candidate.content = MagicMock()
        mock_candidate.content.parts = [mock_part]
        mock_candidate.grounding_metadata = mock_gm

        mock_usage = MagicMock()
        mock_usage.prompt_token_count = 10
        mock_usage.candidates_token_count = 20
        mock_usage.total_token_count = 30
        mock_usage.thinking_token_count = 0

        mock_chunk = MagicMock()
        mock_chunk.candidates = [mock_candidate]
        mock_chunk.usage_metadata = mock_usage

        async def mock_stream():
            yield mock_chunk

        mock_client.aio.models.generate_content_stream = AsyncMock(return_value=mock_stream())

        request = pb.GenerateRequest(
            session_id="sess-1",
            execution_id="exec-1",
            llm_config=pb.LLMConfig(
                backend="google-native",
                model="gemini-2.5-pro",
                api_key_env="TEST_API_KEY",
            ),
            messages=[pb.ConversationMessage(role="user", content="Who won Euro 2024?")],
        )

        responses = []
        async for resp in provider.generate(request):
            responses.append(resp)

        # Expected order: text, grounding, usage, is_final
        assert len(responses) == 4
        assert responses[0].HasField("text")
        assert responses[1].HasField("grounding")
        assert list(responses[1].grounding.web_search_queries) == ["Euro 2024 winner"]
        assert responses[2].HasField("usage")
        assert responses[3].is_final
