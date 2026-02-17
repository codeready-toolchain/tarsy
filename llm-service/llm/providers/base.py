"""Base LLM provider interface."""
from abc import ABC, abstractmethod
from typing import AsyncIterator

from llm_proto import llm_service_pb2 as pb


class LLMProvider(ABC):
    """Abstract base class for LLM providers.

    Each provider wraps a specific LLM SDK and translates between
    the proto GenerateRequest format and the provider's native API.
    Providers are stateless except for cached SDK clients.
    """

    @abstractmethod
    async def generate(
        self,
        request: pb.GenerateRequest,
    ) -> AsyncIterator[pb.GenerateResponse]:
        """Stream LLM responses for the given request.

        Yields GenerateResponse chunks (text, thinking, tool_call, usage, error).
        The last chunk must have is_final=True.
        """
        ...
