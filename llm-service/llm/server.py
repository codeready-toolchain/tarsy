"""gRPC server entry point for LLM service."""
import asyncio
import logging
import os
import signal
from pathlib import Path

import grpc
from dotenv import load_dotenv
from grpc_health.v1 import health, health_pb2, health_pb2_grpc

from llm_proto import llm_service_pb2_grpc as pb_grpc
from llm.servicer import LLMServicer

# Configure logging
logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(name)s] %(levelname)s: %(message)s",
)
logger = logging.getLogger(__name__)


# Address of the TARSy backend's HTTP server, which it closes last during shutdown.
# On SIGTERM the LLM service keeps serving until this address stops accepting
# connections, so the backend can finish draining sessions. Empty disables the wait.
DEFAULT_SHUTDOWN_WAIT_ADDR = "localhost:8080"


async def wait_until_closed(addr: str, interval: float = 2.0, connect_timeout: float = 1.0) -> None:
    """Return once TCP connections to addr (host:port) are refused, i.e. nothing listens there.

    Other errors keep the wait going: stopping early would cut off a backend that is
    still draining, while waiting too long is bounded by the termination grace period.
    """
    host, _, port = addr.rpartition(":")
    warned = False
    while True:
        try:
            _, writer = await asyncio.wait_for(
                # all_errors: one error per resolved address (localhost is ::1 and 127.0.0.1);
                # otherwise they are merged into a plain OSError.
                asyncio.open_connection(host, int(port), all_errors=True),
                timeout=connect_timeout,
            )
        except TimeoutError:  # a subclass of OSError: keep before it
            pass  # slow to accept, but still there
        except (ExceptionGroup, OSError) as err:
            errors = err.exceptions if isinstance(err, ExceptionGroup) else (err,)
            if all(isinstance(e, ConnectionRefusedError) for e in errors):
                return
            if not warned:
                logger.warning("Checking %s failed (%s), still waiting", addr, err)
                warned = True
        else:
            writer.close()
            await writer.wait_closed()
        await asyncio.sleep(interval)


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

    # Add LLM servicer
    servicer = LLMServicer()
    pb_grpc.add_LLMServiceServicer_to_server(servicer, server)

    # Add gRPC health service (used by K8s probes)
    health_servicer = health.aio.HealthServicer()
    health_pb2_grpc.add_HealthServicer_to_server(health_servicer, server)

    server.add_insecure_port(f"[::]:{port}")
    await server.start()

    # Mark as serving only after server is fully started
    await health_servicer.set("", health_pb2.HealthCheckResponse.SERVING)
    logger.info("LLM gRPC server listening on port %d (health: SERVING)", port)

    # SIGTERM: keep serving until the backend is gone. SIGINT: stop now.
    # Without a handler, SIGTERM is ignored when this process is PID 1 in its container.
    loop = asyncio.get_running_loop()
    stop_signal: asyncio.Future = loop.create_future()
    for sig in (signal.SIGTERM, signal.SIGINT):
        loop.add_signal_handler(sig, lambda s=sig: stop_signal.done() or stop_signal.set_result(s))
    sig = await stop_signal
    for signum in (signal.SIGTERM, signal.SIGINT):
        loop.remove_signal_handler(signum)  # a second signal now uses the default action

    wait_addr = os.getenv("SHUTDOWN_WAIT_ADDR", DEFAULT_SHUTDOWN_WAIT_ADDR)
    if sig == signal.SIGTERM and wait_addr:
        logger.info("SIGTERM received, serving until %s closes", wait_addr)
        await wait_until_closed(wait_addr)

    logger.info("Stopping LLM gRPC server")
    await server.stop(grace=5)


def main():
    """Main entry point."""
    port = int(os.getenv("GRPC_PORT", "50051"))
    asyncio.run(serve(port))


if __name__ == "__main__":
    main()
