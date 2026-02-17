"""Tests for LLMServicer."""
import pytest
from unittest.mock import AsyncMock, MagicMock, Mock, patch

from llm_proto import llm_service_pb2 as pb
from llm.servicer import LLMServicer

pytestmark = pytest.mark.unit


@pytest.fixture
def mock_provider():
    """Create a mock LLM provider."""
    provider = AsyncMock()
    return provider


@pytest.fixture
def servicer_with_mock_provider(mock_provider):
    """Create a servicer with a mocked provider registry.
    
    Note: Both patching ProviderRegistry and manually overriding _registry are needed:
    - The patch prevents side effects during __init__ (e.g., GoogleNativeProvider instantiation)
    - The manual assignment ensures the mock is properly in place for test assertions
    """
    with patch("llm.servicer.ProviderRegistry") as mock_registry_class:
        mock_registry = Mock()
        mock_registry.get.return_value = mock_provider
        mock_registry_class.return_value = mock_registry
        
        servicer = LLMServicer()
        servicer._registry = mock_registry
        
        yield servicer, mock_registry, mock_provider


class TestLLMServicer:
    """Test LLMServicer functionality."""

    async def test_generate_uses_default_backend(self, servicer_with_mock_provider):
        """Test that Generate uses 'google-native' as default backend."""
        servicer, mock_registry, mock_provider = servicer_with_mock_provider
        
        async def mock_generate(request):
            yield pb.GenerateResponse(text=pb.TextDelta(content="Response"), is_final=True)
        
        mock_provider.generate = mock_generate
        
        request = pb.GenerateRequest(
            session_id="sess-1",
            execution_id="exec-1",
            llm_config=pb.LLMConfig(model="gemini-2.5-pro"),
            messages=[],
        )
        context = MagicMock()
        
        responses = []
        async for resp in servicer.Generate(request, context):
            responses.append(resp)
        
        mock_registry.get.assert_called_once_with("google-native")
        assert len(responses) == 1

    async def test_generate_uses_specified_backend(self, servicer_with_mock_provider):
        """Test that Generate uses the backend specified in the request."""
        servicer, mock_registry, mock_provider = servicer_with_mock_provider
        
        async def mock_generate(request):
            yield pb.GenerateResponse(text=pb.TextDelta(content="Response"), is_final=True)
        
        mock_provider.generate = mock_generate
        
        request = pb.GenerateRequest(
            session_id="sess-1",
            execution_id="exec-1",
            llm_config=pb.LLMConfig(backend="custom-backend", model="model-1"),
            messages=[],
        )
        context = MagicMock()
        
        responses = []
        async for resp in servicer.Generate(request, context):
            responses.append(resp)
        
        mock_registry.get.assert_called_once_with("custom-backend")

    async def test_generate_invalid_backend(self, servicer_with_mock_provider):
        """Test that Generate yields error response for invalid backend."""
        servicer, mock_registry, _ = servicer_with_mock_provider
        
        # Configure the mock to raise on invalid backend
        mock_registry.get.side_effect = ValueError("No provider registered for backend 'invalid'")
        
        request = pb.GenerateRequest(
            session_id="sess-1",
            execution_id="exec-1",
            llm_config=pb.LLMConfig(backend="invalid", model="model-1"),
            messages=[],
        )
        context = MagicMock()
        
        responses = []
        async for resp in servicer.Generate(request, context):
            responses.append(resp)
        
        assert len(responses) == 1
        assert responses[0].HasField("error")
        assert responses[0].error.code == "invalid_backend"
        assert responses[0].error.retryable is False
        assert responses[0].is_final

    async def test_generate_streams_provider_responses(self, servicer_with_mock_provider):
        """Test that Generate streams all responses from the provider."""
        servicer, mock_registry, mock_provider = servicer_with_mock_provider
        
        async def mock_generate(request):
            yield pb.GenerateResponse(text=pb.TextDelta(content="Hello"))
            yield pb.GenerateResponse(text=pb.TextDelta(content=" world"))
            yield pb.GenerateResponse(is_final=True)
        
        mock_provider.generate = mock_generate
        
        request = pb.GenerateRequest(
            session_id="sess-1",
            execution_id="exec-1",
            llm_config=pb.LLMConfig(model="gemini-2.5-pro"),
            messages=[],
        )
        context = MagicMock()
        
        responses = []
        async for resp in servicer.Generate(request, context):
            responses.append(resp)
        
        assert len(responses) == 3
        assert responses[0].text.content == "Hello"
        assert responses[1].text.content == " world"
        assert responses[2].is_final

    async def test_generate_provider_exception(self, servicer_with_mock_provider):
        """Test that Generate handles provider exceptions and yields error response."""
        servicer, mock_registry, mock_provider = servicer_with_mock_provider
        
        async def mock_generate(request):
            raise RuntimeError("Provider failed")
            yield
        
        mock_provider.generate = mock_generate
        
        request = pb.GenerateRequest(
            session_id="sess-1",
            execution_id="exec-1",
            llm_config=pb.LLMConfig(model="gemini-2.5-pro"),
            messages=[],
        )
        context = MagicMock()
        
        responses = []
        async for resp in servicer.Generate(request, context):
            responses.append(resp)
        
        assert len(responses) == 1
        assert responses[0].HasField("error")
        assert responses[0].error.code == "internal"
        assert responses[0].error.message == "Internal error during generation"
        assert responses[0].is_final

    async def test_generate_passes_request_to_provider(self, servicer_with_mock_provider):
        """Test that Generate passes the full request to the provider."""
        servicer, mock_registry, mock_provider = servicer_with_mock_provider
        
        captured_request = None
        
        async def mock_generate(request):
            nonlocal captured_request
            captured_request = request
            yield pb.GenerateResponse(is_final=True)
        
        mock_provider.generate = mock_generate
        
        messages = [pb.ConversationMessage(role="user", content="Test")]
        request = pb.GenerateRequest(
            session_id="sess-123",
            execution_id="exec-456",
            llm_config=pb.LLMConfig(model="gemini-2.5-pro"),
            messages=messages,
        )
        context = MagicMock()
        
        async for _ in servicer.Generate(request, context):
            pass
        
        assert captured_request is request
        assert captured_request.session_id == "sess-123"
        assert captured_request.execution_id == "exec-456"
