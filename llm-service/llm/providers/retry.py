"""Shared identical-retry wait for LLM providers."""

from __future__ import annotations

import asyncio
import random
from typing import Optional

from llm.providers.http_status import extract_retry_hint

MAX_RETRIES = 3
RETRY_BACKOFF_BASE = 2  # seconds
RETRY_AFTER_CAP = 60.0
JITTER_CAP = 8.0


def retry_delay(attempt: int, hint: Optional[float]) -> float:
    """Seconds to wait before the next identical retry."""
    if hint is not None and 0 < hint <= RETRY_AFTER_CAP:
        return hint
    return random.uniform(0, min(JITTER_CAP, RETRY_BACKOFF_BASE ** attempt))


async def sleep_before_retry(attempt: int, err: BaseException) -> None:
    """Sleep unless this was the last attempt. CancelledError must propagate."""
    if attempt >= MAX_RETRIES - 1:
        return
    await asyncio.sleep(retry_delay(attempt, extract_retry_hint(err)))
