"""gRPC server entry point for LLM service."""
import asyncio
import os
import sys
from pathlib import Path

import grpc
from dotenv import load_dotenv

from proto import llm_service_pb2_grpc as pb_grpc
from llm.servicer import LLMServicer


async def serve(port: int = 50051):
    """Start the gRPC server."""
    # Load .env file from deploy directory (relative to project root)
    env_path = Path(__file__).parent.parent.parent / "deploy" / ".env"
    if env_path.exists():
        load_dotenv(env_path)
        print(f"Loaded environment variables from {env_path}")
    else:
        print(f"Warning: .env file not found at {env_path}")
        print("Continuing with existing environment variables...")
    
    print(f"Starting LLM gRPC server on port {port}")
    print("Credentials will be resolved per-request from environment variables")
    
    # Create gRPC server
    server = grpc.aio.server()
    
    # Add servicer (no hardcoded credentials)
    servicer = LLMServicer()
    pb_grpc.add_LLMServiceServicer_to_server(servicer, server)
    
    # Bind port
    server.add_insecure_port(f"[::]:{port}")
    
    # Start server
    await server.start()
    print(f"LLM gRPC server listening on port {port}")
    
    # Wait for termination
    await server.wait_for_termination()


def main():
    """Main entry point."""
    # Get port from environment (loaded from .env in serve())
    port = int(os.getenv("GRPC_PORT", "50051"))
    asyncio.run(serve(port))


if __name__ == "__main__":
    main()
