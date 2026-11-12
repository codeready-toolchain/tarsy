"""Tests for the gRPC server entry point."""
import asyncio
import errno

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

    async def test_returns_when_all_addresses_of_a_name_refuse(self):
        # localhost resolves to ::1 and 127.0.0.1: both refusals must count.
        server = await asyncio.start_server(lambda r, w: w.close(), "localhost", 0)
        port = server.sockets[0].getsockname()[1]
        server.close()
        await server.wait_closed()

        await asyncio.wait_for(wait_until_closed(f"localhost:{port}", interval=0.01), timeout=1)

    async def test_keeps_waiting_on_other_errors(self, monkeypatch):
        refused = ConnectionRefusedError(errno.ECONNREFUSED, "refused")
        failures = [
            OSError(errno.EMFILE, "too many open files"),
            ExceptionGroup("create_connection failed", [refused, OSError(errno.ENETUNREACH, "unreachable")]),
            ExceptionGroup("create_connection failed", [refused]),
        ]
        calls = 0

        async def open_connection(*args, **kwargs):
            nonlocal calls
            calls += 1
            raise failures.pop(0)

        monkeypatch.setattr("llm.server.asyncio.open_connection", open_connection)

        await asyncio.wait_for(wait_until_closed("127.0.0.1:1", interval=0.01), timeout=1)
        assert calls == 3
