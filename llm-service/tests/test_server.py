"""Tests for the gRPC server entry point."""
import asyncio

import pytest

from llm.server import wait_until_closed

pytestmark = pytest.mark.unit


async def _listen():
    server = await asyncio.start_server(lambda r, w: w.close(), "127.0.0.1", 0)
    port = server.sockets[0].getsockname()[1]
    return server, f"127.0.0.1:{port}"


class TestWaitUntilClosed:
    async def test_returns_when_nothing_listens(self):
        server, addr = await _listen()
        server.close()
        await server.wait_closed()

        await asyncio.wait_for(wait_until_closed(addr, interval=0.01), timeout=1)

    async def test_waits_while_listening_and_returns_after_close(self):
        server, addr = await _listen()
        waiter = asyncio.ensure_future(wait_until_closed(addr, interval=0.01))

        await asyncio.sleep(0.1)
        assert not waiter.done()

        server.close()
        await server.wait_closed()
        await asyncio.wait_for(waiter, timeout=1)
