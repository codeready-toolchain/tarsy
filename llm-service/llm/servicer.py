"""gRPC servicer implementation for LLM service."""
import logging
from typing import AsyncIterator

import grpc

from proto import llm_service_pb2 as pb
from proto import llm_service_pb2_grpc as pb_grpc
from llm.providers.registry import ProviderRegistry
from llm.providers.google_native import GoogleNativeProvider

logger = logging.getLogger(__name__)


class LLMServicer(pb_grpc.LLMServiceServicer):
    """gRPC servicer that routes Generate requests via the provider registry."""

    def __init__(self):
        """Initialize the servicer with the provider registry."""
        self._registry = ProviderRegistry()

        # Register providers
        self._registry.register("google-native", GoogleNativeProvider())
        # Future: self._registry.register("langchain", LangChainProvider())

        logger.info("LLM Servicer initialized with providers: google-native")

    async def Generate(
        self,
        request: pb.GenerateRequest,
        context: grpc.aio.ServicerContext,
    ) -> AsyncIterator[pb.GenerateResponse]:
        """Route Generate request to the appropriate provider."""
        backend = request.llm_config.backend or "google-native"
        logger.info(
            "Generate: session=%s execution=%s backend=%s model=%s",
            request.session_id,
            request.execution_id,
            backend,
            request.llm_config.model,
        )

        try:
            provider = self._registry.get(backend)
        except ValueError as e:
            yield pb.GenerateResponse(
                error=pb.ErrorInfo(
                    message=str(e),
                    code="invalid_backend",
                    retryable=False,
                ),
                is_final=True,
            )
            return

        try:
            async for chunk in provider.generate(request):
                yield chunk
        except Exception:
            logger.exception(
                "Unhandled error in Generate for session %s",
                request.session_id,
            )
            yield pb.GenerateResponse(
                error=pb.ErrorInfo(
                    message="Internal error during generation",
                    code="internal",
                    retryable=False,
                ),
                is_final=True,
            )
