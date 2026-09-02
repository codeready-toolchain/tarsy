"""Provider prompt-cache helpers for looping LangChain Generate calls.

Go ANDs eligibility with the cluster toggle onto GenerateRequest.prompt_cache.
This module only classifies the backend and builds marker payloads.
"""

from __future__ import annotations

import re
from typing import List, Optional, Tuple

from llm_proto import llm_service_pb2 as pb

NONE = "none"
ANTHROPIC = "anthropic"
OPENAI_EXPLICIT = "openai_explicit"
OPENAI_EXPLICIT_DISABLE = "openai_explicit_disable"

_GPT56_RE = re.compile(r"^gpt-5\.(\d+)", re.IGNORECASE)

CACHE_CONTROL_1H = {"type": "ephemeral", "ttl": "1h"}
CACHE_CONTROL_NO_TTL = {"type": "ephemeral"}
PROMPT_CACHE_BREAKPOINT = {"mode": "explicit"}


def is_anthropic_claude(config: pb.LLMConfig) -> bool:
    provider = (config.provider or "").lower()
    if provider == "anthropic":
        return True
    if provider == "vertexai":
        model = (config.model or "").lower()
        return "claude" in model or "anthropic" in model
    return False


def is_openai_explicit_cache_model(model: str) -> bool:
    """GPT-5.6+ (gpt-5. with integer minor >= 6), including dated/variant suffixes."""
    match = _GPT56_RE.match(model or "")
    return match is not None and int(match.group(1)) >= 6


def classify_cache(config: pb.LLMConfig, prompt_cache: bool, execution_id: str) -> str:
    provider = (config.provider or "").lower()
    if provider == "openai" and is_openai_explicit_cache_model(config.model):
        if prompt_cache and execution_id:
            return OPENAI_EXPLICIT
        return OPENAI_EXPLICIT_DISABLE
    if not prompt_cache:
        return NONE
    if is_anthropic_claude(config):
        return ANTHROPIC
    return NONE


def cache_control(strip_ttl: bool) -> dict:
    return dict(CACHE_CONTROL_NO_TTL) if strip_ttl else dict(CACHE_CONTROL_1H)


def openai_prompt_cache_options(strip_ttl: bool) -> dict:
    options = {"mode": "explicit"}
    if not strip_ttl:
        options["ttl"] = "30m"
    return options


def first_user_index(messages: List[pb.ConversationMessage]) -> int:
    """Index of the first user message, or -1 if none."""
    for i, msg in enumerate(messages):
        if msg.role == "user":
            return i
    return -1


def last_tool_index(messages: List[pb.ConversationMessage]) -> int:
    """Index of the last tool-result message, or -1 if none."""
    for i in range(len(messages) - 1, -1, -1):
        if messages[i].role == "tool":
            return i
    return -1


def cache_degrade_sequence(kind: str) -> List[Tuple[str, bool]]:
    """(cache_kind, strip_ttl) attempts. First is full markers; then TTL strip; then none."""
    if kind == NONE:
        return [(NONE, False)]
    return [(kind, False), (kind, True), (NONE, False)]


def is_bad_request(err: BaseException) -> bool:
    """True when the provider rejected extra cache fields (HTTP 400)."""
    seen: set[int] = set()
    current: Optional[BaseException] = err
    while current is not None and id(current) not in seen:
        seen.add(id(current))
        status = getattr(current, "status_code", None)
        if status is None:
            status = getattr(current, "status", None)
        if status == 400:
            return True
        if "BadRequest" in type(current).__name__:
            return True
        current = current.__cause__ or current.__context__
    return False
