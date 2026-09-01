"""HTTP status and retry classification for provider errors."""

from __future__ import annotations

import errno
import re
import ssl
from collections import deque
from datetime import datetime, timezone
from email.utils import parsedate_to_datetime
from typing import Iterator, Optional

import httpx

_HTTP_MIN = 100
_HTTP_MAX = 599
_ERROR_CODE_RE = re.compile(r"Error code:\s*(\d+)")
_RETRYABLE_EXACT = {404, 408, 429}
_RETRY_HINT_CAP = 60.0
_SPEND_CAP_TYPE = "usage_limit_reached"
_TLS_VERIFY_MSG = "CERTIFICATE_VERIFY_FAILED"
_FALSE_HEADER_VALUES = frozenset({"false", "0", "no"})
_CONNECTION_FRAGMENTS = (
    "ECONNRESET",
    "ETIMEDOUT",
    "EPIPE",
    "ENOTFOUND",
    "EAI_AGAIN",
    "ECONNREFUSED",
)
_CONNECTION_ERRNOS = {
    code
    for code in (
        getattr(errno, "ECONNRESET", None),
        getattr(errno, "ETIMEDOUT", None),
        getattr(errno, "EPIPE", None),
        getattr(errno, "ECONNREFUSED", None),
        getattr(errno, "EAI_AGAIN", None),
    )
    if code is not None
}
_CONNECTION_TYPES = (
    httpx.ConnectError,
    httpx.ConnectTimeout,
    httpx.ReadTimeout,
    httpx.RemoteProtocolError,
    httpx.LocalProtocolError,
)
_DURATION_RE = re.compile(
    r"^\s*(\d+(?:\.\d+)?)\s*(ms|s|m|h)?\s*$",
    re.IGNORECASE,
)


def _http_status(value: object) -> Optional[int]:
    if isinstance(value, int) and not isinstance(value, bool) and _HTTP_MIN <= value <= _HTTP_MAX:
        return value
    return None


def _iter_exceptions(err: BaseException) -> Iterator[BaseException]:
    seen: set[int] = set()
    pending: deque[BaseException] = deque([err])
    while pending:
        current = pending.popleft()
        if id(current) in seen:
            continue
        seen.add(id(current))
        yield current
        if current.__cause__ is not None:
            pending.append(current.__cause__)
        if current.__context__ is not None:
            pending.append(current.__context__)


def extract_http_status(err: BaseException) -> Optional[int]:
    """First HTTP status found on err or its cause/context chain."""
    for current in _iter_exceptions(err):
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
    return None


def _header_value(value: object) -> Optional[str]:
    if value is None:
        return None
    if isinstance(value, (list, tuple)):
        value = value[0] if value else None
    if value is None:
        return None
    if isinstance(value, bytes):
        return value.decode()
    return str(value)


def _header_get(headers: object, name: str) -> Optional[str]:
    if headers is None:
        return None
    target = name.lower()
    getter = getattr(headers, "get", None)
    if callable(getter):
        value = getter(name)
        if value is None:
            value = getter(target)
        if value is not None:
            return _header_value(value)
    items = getattr(headers, "items", None)
    if callable(items):
        try:
            for key, value in items():
                if str(key).lower() == target:
                    return _header_value(value)
        except (TypeError, ValueError):
            return None
    return None


def _parse_duration(value: object) -> Optional[float]:
    if value is None or isinstance(value, bool):
        return None
    if isinstance(value, (int, float)):
        return float(value) if value > 0 else None
    text = str(value).strip()
    if not text:
        return None
    match = _DURATION_RE.fullmatch(text)
    if match is None:
        return None
    amount = float(match.group(1))
    unit = (match.group(2) or "s").lower()
    if unit == "ms":
        amount /= 1000.0
    elif unit == "m":
        amount *= 60.0
    elif unit == "h":
        amount *= 3600.0
    return amount if amount > 0 else None


def _parse_retry_after(value: str) -> Optional[float]:
    duration = _parse_duration(value)
    if duration is not None:
        return duration
    try:
        when = parsedate_to_datetime(value)
    except (TypeError, ValueError, OverflowError, IndexError):
        return None
    if when.tzinfo is None:
        when = when.replace(tzinfo=timezone.utc)
    delta = (when - datetime.now(timezone.utc)).total_seconds()
    return delta if delta > 0 else None


def _parse_retry_after_ms(value: str) -> Optional[float]:
    text = value.strip()
    try:
        ms = float(text)
    except ValueError:
        return _parse_duration(text)
    return ms / 1000.0 if ms > 0 else None


def _retry_delay_from_obj(obj: object, depth: int = 0) -> Optional[float]:
    if depth > 8 or obj is None:
        return None
    if isinstance(obj, dict):
        delay = obj.get("retryDelay", obj.get("retry_delay"))
        if delay is not None:
            parsed = _parse_duration(delay)
            if parsed is not None:
                return parsed
        for value in obj.values():
            found = _retry_delay_from_obj(value, depth + 1)
            if found is not None:
                return found
    elif isinstance(obj, (list, tuple)):
        for item in obj:
            found = _retry_delay_from_obj(item, depth + 1)
            if found is not None:
                return found
    return None


def _headers_of(current: BaseException) -> object:
    response = getattr(current, "response", None)
    if response is not None:
        headers = getattr(response, "headers", None)
        if headers is not None:
            return headers
    return getattr(current, "headers", None)


def extract_retry_hint(err: BaseException) -> Optional[float]:
    """Server-informed wait in seconds, or None to use jitter."""
    for current in _iter_exceptions(err):
        headers = _headers_of(current)
        retry_after = _header_get(headers, "retry-after")
        if retry_after is not None:
            parsed = _parse_retry_after(retry_after)
            if parsed is not None:
                return parsed
        retry_after_ms = _header_get(headers, "retry-after-ms")
        if retry_after_ms is not None:
            parsed = _parse_retry_after_ms(retry_after_ms)
            if parsed is not None:
                return parsed
        for attr in ("details", "body"):
            found = _retry_delay_from_obj(getattr(current, attr, None))
            if found is not None:
                return found
    return None


def _is_tls_cert_failure(err: BaseException) -> bool:
    for current in _iter_exceptions(err):
        if isinstance(current, ssl.SSLCertVerificationError):
            return True
        if _TLS_VERIFY_MSG in str(current):
            return True
    return False


def _is_spend_cap(err: BaseException) -> bool:
    for current in _iter_exceptions(err):
        should_retry = _header_get(_headers_of(current), "x-should-retry")
        if should_retry is not None and should_retry.strip().lower() in _FALSE_HEADER_VALUES:
            return True
        if _SPEND_CAP_TYPE in str(current).lower():
            return True
        for attr in ("details", "body", "type"):
            value = getattr(current, attr, None)
            if value is not None and _SPEND_CAP_TYPE in str(value).lower():
                return True
    return False


def _is_connection_error(err: BaseException) -> bool:
    for current in _iter_exceptions(err):
        if isinstance(current, _CONNECTION_TYPES):
            return True
        if isinstance(current, OSError) and getattr(current, "errno", None) in _CONNECTION_ERRNOS:
            return True
        message = str(current)
        if any(fragment in message for fragment in _CONNECTION_FRAGMENTS):
            return True
    return False


def is_retryable(err: BaseException) -> bool:
    """True when the same Generate is safe to retry (zero chunks already enforced by callers)."""
    if _is_tls_cert_failure(err):
        return False
    if _is_spend_cap(err):
        return False
    hint = extract_retry_hint(err)
    if hint is not None and hint > _RETRY_HINT_CAP:
        return False
    if _is_connection_error(err):
        return True
    status = extract_http_status(err)
    if status is None:
        return False
    return status in _RETRYABLE_EXACT or 500 <= status <= 599
