"""gRPC server entry point for LLM service."""
import asyncio
import logging
import os
import sys
from pathlib import Path

import grpc
from dotenv import load_dotenv

from proto import llm_service_pb2_grpc as pb_grpc
from llm.servicer import LLMServicer

# Configure logging
logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(name)s] %(levelname)s: %(message)s",
)
logger = logging.getLogger(__name__)


async def serve(port: int = 50051):
    """Start the gRPC server."""
    # Load .env file from deploy/config directory (relative to project root)
    env_path = Path(__file__).parent.parent.parent / "deploy" / "config" / ".env"
    if env_path.exists():
        load_dotenv(env_path)
        logger.info("Loaded environment variables from %s", env_path)
    else:
        logger.warning(".env file not found at %s", env_path)

    logger.info("Starting LLM gRPC server on port %d", port)

    # Create gRPC server
    server = grpc.aio.server()

    # Add servicer
    servicer = LLMServicer()
    pb_grpc.add_LLMServiceServicer_to_server(servicer, server)

    # Bind port
    server.add_insecure_port(f"[::]:{port}")

    # Start server
    await server.start()
    logger.info("LLM gRPC server listening on port %d", port)

    # Wait for termination
    await server.wait_for_termination()


def main():
    """Main entry point."""
    port = int(os.getenv("GRPC_PORT", "50051"))
    asyncio.run(serve(port))


if __name__ == "__main__":
    main()
