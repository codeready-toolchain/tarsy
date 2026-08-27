"""Extract cache token counts and normalize proto input_tokens to uncached input.

Callers last-win cache fields only when extract_cache_tokens returns a tuple
(keys were present). A later output-only chunk with no cache keys returns None
so an earlier cache read is not wiped.

Anthropic raw usage.input_tokens is already uncached. LangChain unified
usage_metadata.input_tokens, OpenAI, and Google prompt counts include cache.
"""

from typing import Any, Mapping, Optional, Tuple

_CACHE_READ_KEYS = ("cache_read_input_tokens", "cached_tokens", "cache_read")
_CACHE_CREATE_KEYS = ("cache_creation_input_tokens", "cache_write_tokens", "cache_creation")
_EPHEMERAL_KEYS = ("ephemeral_5m_input_tokens", "ephemeral_1h_input_tokens")
_DETAILS_KEYS = ("input_token_details", "prompt_tokens_details", "input_tokens_details")
_RAW_USAGE_KEYS = ("usage", "token_usage")
_ANTHROPIC_RAW_CACHE_KEYS = ("cache_read_input_tokens", "cache_creation_input_tokens")


def extract_cache_tokens(
    usage_metadata: Any = None,
    response_metadata: Any = None,
) -> Optional[Tuple[int, int]]:
    """Return (cache_read, cache_creation) if cache keys are present, else None.

    Prefers provider raw ``response_metadata["usage"]`` (or ``token_usage``)
    over LangChain unified ``usage_metadata`` when the raw blob has cache keys.
    """
    raw = _first_attr(response_metadata, _RAW_USAGE_KEYS)
    if raw is not None and _has_cache_keys(raw):
        return _extract_from_usage(raw)
    if usage_metadata is not None and _has_cache_keys(usage_metadata):
        return _extract_from_usage(usage_metadata)
    return None


def extract_anthropic_raw_input(response_metadata: Any = None) -> Optional[int]:
    """Return already-uncached Anthropic raw ``usage.input_tokens``, else None.

    Only treats the blob as Anthropic when native cache keys are present so
    OpenAI inclusive ``input_tokens`` is not mistaken for uncached.
    """
    raw = _first_attr(response_metadata, _RAW_USAGE_KEYS)
    if raw is None or not _any_key(raw, _ANTHROPIC_RAW_CACHE_KEYS):
        return None
    if not _has(raw, "input_tokens"):
        return None
    return _int(_get(raw, "input_tokens"))


def uncached_input_tokens(
    inclusive_input: int,
    cache_read: int,
    cache_creation: int,
    anthropic_raw_input: Optional[int] = None,
) -> int:
    """Return billed uncached input for proto UsageInfo.input_tokens."""
    if anthropic_raw_input is not None:
        return max(anthropic_raw_input, 0)
    return max(inclusive_input - cache_read - cache_creation, 0)


def _extract_from_usage(usage: Any) -> Tuple[int, int]:
    read = _first_int(usage, _CACHE_READ_KEYS)
    create = _first_int(usage, _CACHE_CREATE_KEYS)

    for key in _DETAILS_KEYS:
        details = _get(usage, key)
        if details is None:
            continue
        if read is None:
            read = _first_int(details, ("cache_read", "cached_tokens", "cache_read_input_tokens"))
        if create is None:
            create = _first_int(details, ("cache_creation", "cache_write_tokens"))

    ephemeral = _ephemeral_sum(usage)
    for key in _DETAILS_KEYS:
        ephemeral += _ephemeral_sum(_get(usage, key))

    if not create and ephemeral:
        create = ephemeral
    if read is None:
        read = 0
    if create is None:
        create = 0
    return read, create


def _has_cache_keys(usage: Any) -> bool:
    if _any_key(usage, _CACHE_READ_KEYS + _CACHE_CREATE_KEYS + _EPHEMERAL_KEYS):
        return True
    for key in _DETAILS_KEYS:
        details = _get(usage, key)
        if details is not None and _any_key(
            details, _CACHE_READ_KEYS + _CACHE_CREATE_KEYS + _EPHEMERAL_KEYS
        ):
            return True
    return False


def _ephemeral_sum(obj: Any) -> int:
    if obj is None or not _any_key(obj, _EPHEMERAL_KEYS):
        return 0
    return _int(_get(obj, "ephemeral_5m_input_tokens")) + _int(
        _get(obj, "ephemeral_1h_input_tokens")
    )


def _first_attr(obj: Any, keys: tuple[str, ...]) -> Any:
    if obj is None:
        return None
    for key in keys:
        value = _get(obj, key)
        if value is not None:
            return value
    return None


def _first_int(obj: Any, keys: tuple[str, ...]) -> Optional[int]:
    if obj is None:
        return None
    for key in keys:
        if _has(obj, key):
            return _int(_get(obj, key))
    return None


def _any_key(obj: Any, keys: tuple[str, ...]) -> bool:
    return any(_has(obj, key) for key in keys)


def _has(obj: Any, key: str) -> bool:
    if obj is None:
        return False
    if isinstance(obj, Mapping):
        return key in obj and obj[key] is not None
    if not hasattr(obj, key):
        return False
    value = getattr(obj, key)
    return value is not None and not callable(value)


def _get(obj: Any, key: str) -> Any:
    if obj is None:
        return None
    if isinstance(obj, Mapping):
        return obj.get(key)
    return getattr(obj, key, None)


def _int(value: Any) -> int:
    if value is None or isinstance(value, bool):
        return 0
    if isinstance(value, int):
        return value
    try:
        return int(value)
    except (TypeError, ValueError):
        return 0
