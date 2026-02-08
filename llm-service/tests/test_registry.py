"""Tests for ProviderRegistry."""
import pytest
from unittest.mock import Mock

from llm.providers.registry import ProviderRegistry
from llm.providers.base import LLMProvider

pytestmark = pytest.mark.unit


class TestProviderRegistry:
    """Test ProviderRegistry functionality."""

    def test_register_and_get_provider(self):
        """Test registering and retrieving a provider."""
        registry = ProviderRegistry()
        mock_provider = Mock(spec=LLMProvider)
        
        registry.register("test-backend", mock_provider)
        result = registry.get("test-backend")
        
        assert result is mock_provider

    def test_get_nonexistent_provider_raises_error(self):
        """Test that getting an unregistered backend raises ValueError."""
        registry = ProviderRegistry()
        
        with pytest.raises(ValueError, match="No provider registered for backend 'nonexistent'"):
            registry.get("nonexistent")

    def test_error_message_includes_available_backends(self):
        """Test that error message lists available backends."""
        registry = ProviderRegistry()
        mock_provider = Mock(spec=LLMProvider)
        
        registry.register("backend-a", mock_provider)
        registry.register("backend-b", mock_provider)
        
        with pytest.raises(ValueError, match="Available backends: backend-a, backend-b"):
            registry.get("missing")

    def test_empty_registry_error_message(self):
        """Test error message when no providers are registered."""
        registry = ProviderRegistry()
        
        with pytest.raises(ValueError, match="Available backends: \\(none\\)"):
            registry.get("any")

    def test_register_multiple_providers(self):
        """Test registering multiple providers with different backends."""
        registry = ProviderRegistry()
        provider_a = Mock(spec=LLMProvider)
        provider_b = Mock(spec=LLMProvider)
        
        registry.register("backend-a", provider_a)
        registry.register("backend-b", provider_b)
        
        assert registry.get("backend-a") is provider_a
        assert registry.get("backend-b") is provider_b

    def test_overwrite_provider(self):
        """Test that re-registering a backend overwrites the previous provider."""
        registry = ProviderRegistry()
        old_provider = Mock(spec=LLMProvider)
        new_provider = Mock(spec=LLMProvider)
        
        registry.register("backend", old_provider)
        registry.register("backend", new_provider)
        
        assert registry.get("backend") is new_provider
