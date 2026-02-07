"""Provider registry for routing requests to the correct LLM provider."""
from typing import Dict

from llm.providers.base import LLMProvider


class ProviderRegistry:
    """Maps backend names to LLMProvider instances.

    Usage:
        registry = ProviderRegistry()
        registry.register("google-native", GoogleNativeProvider())
        provider = registry.get("google-native")
    """

    def __init__(self):
        self._providers: Dict[str, LLMProvider] = {}

    def register(self, backend: str, provider: LLMProvider) -> None:
        """Register a provider for a backend name."""
        self._providers[backend] = provider

    def get(self, backend: str) -> LLMProvider:
        """Get the provider for a backend name.

        Raises:
            ValueError: If no provider is registered for the backend.
        """
        provider = self._providers.get(backend)
        if provider is None:
            available = ", ".join(sorted(self._providers.keys())) or "(none)"
            raise ValueError(
                f"No provider registered for backend '{backend}'. "
                f"Available backends: {available}"
            )
        return provider
