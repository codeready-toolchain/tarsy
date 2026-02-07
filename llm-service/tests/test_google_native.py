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
