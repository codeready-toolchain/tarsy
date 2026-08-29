"""HTTP status extraction for classifying provider errors as retryable."""

from __future__ import annotations

import re
from typing import Optional

_HTTP_MIN = 100
_HTTP_MAX = 599
_ERROR_CODE_RE = re.compile(r"Error code:\s*(\d+)")
_RETRYABLE_EXACT = {429, 404}


def _http_status(value: object) -> Optional[int]:
    if isinstance(value, int) and not isinstance(value, bool) and _HTTP_MIN <= value <= _HTTP_MAX:
        return value
    return None


def extract_http_status(err: BaseException) -> Optional[int]:
    """First HTTP status found on err or its cause/context chain."""
    seen: set[int] = set()
    current: Optional[BaseException] = err
    while current is not None and id(current) not in seen:
        seen.add(id(current))
        for attr in ("status_code", "code", "status"):
            status = _http_status(getattr(current, attr, None))
            if status is not None:
                return status
        response = getattr(current, "response", None)
        if response is not None:
            status = _http_status(getattr(response, "status_code", None))
            if status is not None:
                return status
        match = _ERROR_CODE_RE.search(str(current))
        if match:
            status = _http_status(int(match.group(1)))
            if status is not None:
                return status
        current = current.__cause__ or current.__context__
    return None


def is_retryable_http(err: BaseException) -> bool:
    """True for HTTP 429, 404, or 5xx — safe to retry with the same request."""
    status = extract_http_status(err)
    if status is None:
        return False
    return status in _RETRYABLE_EXACT or 500 <= status <= 599
