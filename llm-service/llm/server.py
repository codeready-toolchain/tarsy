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
        print(f"Loaded configuration from {env_path}")
    else:
        print(f"Warning: .env file not found at {env_path}")
        print("Continuing with existing environment variables...")
    
    # Get configuration from environment
    api_key = os.getenv("GOOGLE_API_KEY")
    if not api_key:
        print("ERROR: GOOGLE_API_KEY environment variable is required")
        print("Please set it in deploy/.env or as an environment variable")
        sys.exit(1)
    
    model = os.getenv("GEMINI_MODEL", "gemini-2.0-flash-thinking-exp-01-21")
    temperature = float(os.getenv("GEMINI_TEMPERATURE", "1.0"))
    
    print(f"Starting LLM gRPC server on port {port}")
    print(f"Model: {model}")
    print(f"Temperature: {temperature}")
    
    # Create gRPC server
    server = grpc.aio.server()
    
    # Add servicer
    servicer = LLMServicer(api_key=api_key, model=model, temperature=temperature)
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
