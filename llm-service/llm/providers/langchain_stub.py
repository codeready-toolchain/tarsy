"""LangChain stub provider — delegates to GoogleNativeProvider until Phase 6.

This stub exists so the backend routing for "langchain" is correctly wired
from day one. The ReAct and synthesis controllers that use LangChain-backed
models will get real LangChain integration in Phase 6.
"""
import logging
from typing import AsyncIterator

from proto import llm_service_pb2 as pb
from llm.providers.base import LLMProvider
from llm.providers.google_native import GoogleNativeProvider

logger = logging.getLogger(__name__)


class LangChainStubProvider(LLMProvider):
    """Stub: delegates to GoogleNativeProvider until Phase 6."""

    def __init__(self, google_provider: GoogleNativeProvider):
        self._delegate = google_provider

    async def generate(
        self,
        request: pb.GenerateRequest,
    ) -> AsyncIterator[pb.GenerateResponse]:
        """Delegate to GoogleNativeProvider."""
        logger.debug(
            "LangChainStubProvider: delegating to GoogleNativeProvider for session=%s",
            request.session_id,
        )
        async for chunk in self._delegate.generate(request):
            yield chunk
