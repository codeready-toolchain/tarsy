"""Tests for identical-retry wait helper."""

import pytest
from unittest.mock import AsyncMock, patch

from llm.providers.retry import (
    JITTER_CAP,
    MAX_RETRIES,
    RETRY_BACKOFF_BASE,
    retry_delay,
    sleep_before_retry,
)

pytestmark = pytest.mark.unit


class TestRetryDelay:
    def test_honors_hint_within_cap(self):
        assert retry_delay(0, 53.0) == 53.0

    def test_hint_zero_uses_jitter(self):
        with patch("llm.providers.retry.random.uniform", return_value=0.4) as mock_uniform:
            assert retry_delay(0, 0.0) == 0.4
            mock_uniform.assert_called_once_with(
                0, min(JITTER_CAP, RETRY_BACKOFF_BASE ** 0),
            )

    def test_hint_none_uses_jitter(self):
        with patch("llm.providers.retry.random.uniform", return_value=0.25):
            assert retry_delay(1, None) == 0.25

    def test_no_hint_delay_is_bounded(self):
        for attempt in (0, 1, 2):
            ceiling = min(JITTER_CAP, RETRY_BACKOFF_BASE ** attempt)
            for _ in range(20):
                delay = retry_delay(attempt, None)
                assert 0 <= delay <= ceiling


class TestSleepBeforeRetry:
    @pytest.mark.asyncio
    async def test_skips_sleep_on_last_attempt(self):
        with patch("llm.providers.retry.asyncio.sleep", new_callable=AsyncMock) as mock_sleep:
            await sleep_before_retry(MAX_RETRIES - 1, RuntimeError("exhausted"))
            mock_sleep.assert_not_called()

    @pytest.mark.asyncio
    async def test_sleeps_on_earlier_attempt(self):
        with patch("llm.providers.retry.asyncio.sleep", new_callable=AsyncMock) as mock_sleep:
            await sleep_before_retry(0, RuntimeError("blip"))
            mock_sleep.assert_called_once()
            assert 0 <= mock_sleep.call_args[0][0] <= 1
