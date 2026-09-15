"""Tests for prompt-cache classification and degrade helpers."""

import pytest

from llm_proto import llm_service_pb2 as pb
from llm.providers import prompt_cache

pytestmark = pytest.mark.unit


class TestClassifyCache:
    @pytest.mark.parametrize(
        "model,expected",
        [
            ("gpt-5.6", True),
            ("GPT-5.6", True),
            ("gpt-5.6-sol", True),
            ("gpt-5.7", True),
            ("gpt-5.5", False),
            ("gpt-5.2", False),
            ("gpt-5", False),
            ("gpt-5-mini", False),
            ("o4-mini", False),
        ],
    )
    def test_openai_explicit_model_gate(self, model, expected):
        assert prompt_cache.is_openai_explicit_cache_model(model) is expected

    def test_anthropic_provider(self):
        cfg = pb.LLMConfig(provider="anthropic", model="claude-sonnet-4-5")
        assert prompt_cache.is_anthropic_claude(cfg)
        assert prompt_cache.classify_cache(cfg, True, "exec-1") == prompt_cache.ANTHROPIC

    def test_vertex_claude(self):
        cfg = pb.LLMConfig(provider="vertexai", model="claude-sonnet-4-5")
        assert prompt_cache.classify_cache(cfg, True, "exec-1") == prompt_cache.ANTHROPIC

    def test_vertex_gemini_not_claude(self):
        cfg = pb.LLMConfig(provider="vertexai", model="gemini-2.5-pro")
        assert prompt_cache.classify_cache(cfg, True, "exec-1") == prompt_cache.NONE

    def test_openai_gpt56_requires_execution_id(self):
        cfg = pb.LLMConfig(provider="openai", model="gpt-5.6")
        assert prompt_cache.classify_cache(cfg, True, "exec-1") == prompt_cache.OPENAI_IMPLICIT
        assert prompt_cache.classify_cache(cfg, True, "") == prompt_cache.OPENAI_EXPLICIT_DISABLE

    def test_openai_gpt56_prompt_cache_off_is_disable(self):
        cfg = pb.LLMConfig(provider="openai", model="gpt-5.6")
        assert prompt_cache.classify_cache(cfg, False, "exec-1") == prompt_cache.OPENAI_EXPLICIT_DISABLE
        assert prompt_cache.classify_cache(cfg, False, "") == prompt_cache.OPENAI_EXPLICIT_DISABLE

    def test_openai_older_extract_only(self):
        cfg = pb.LLMConfig(provider="openai", model="gpt-5.2")
        assert prompt_cache.classify_cache(cfg, True, "exec-1") == prompt_cache.NONE
        assert prompt_cache.classify_cache(cfg, False, "exec-1") == prompt_cache.NONE

    def test_flag_off(self):
        cfg = pb.LLMConfig(provider="anthropic", model="claude-sonnet-4-5")
        assert prompt_cache.classify_cache(cfg, False, "exec-1") == prompt_cache.NONE

    def test_google_ignored(self):
        cfg = pb.LLMConfig(provider="google", model="gemini-2.5-pro")
        assert prompt_cache.classify_cache(cfg, True, "exec-1") == prompt_cache.NONE


class TestDegradeAndWalkBack:
    def test_degrade_sequence(self):
        assert prompt_cache.cache_degrade_sequence(prompt_cache.NONE) == [
            (prompt_cache.NONE, False),
        ]
        assert prompt_cache.cache_degrade_sequence(prompt_cache.ANTHROPIC) == [
            (prompt_cache.ANTHROPIC, False),
            (prompt_cache.ANTHROPIC, True),
            (prompt_cache.NONE, False),
        ]

        assert prompt_cache.cache_degrade_sequence(prompt_cache.OPENAI_EXPLICIT_DISABLE) == [
            (prompt_cache.OPENAI_EXPLICIT_DISABLE, False),
            (prompt_cache.OPENAI_EXPLICIT_DISABLE, True),
            (prompt_cache.NONE, False),
        ]
        assert prompt_cache.cache_degrade_sequence(prompt_cache.OPENAI_IMPLICIT) == [
            (prompt_cache.OPENAI_IMPLICIT, False),
            (prompt_cache.OPENAI_IMPLICIT, True),
            (prompt_cache.NONE, False),
        ]

    def test_openai_cache_mode_and_options(self):
        assert prompt_cache.openai_cache_mode(prompt_cache.OPENAI_IMPLICIT) == "implicit"
        assert prompt_cache.openai_cache_mode(prompt_cache.OPENAI_EXPLICIT_DISABLE) == "explicit"
        assert prompt_cache.openai_prompt_cache_options(False, "implicit") == {
            "mode": "implicit", "ttl": "30m",
        }
        assert prompt_cache.openai_prompt_cache_options(True, "implicit") == {
            "mode": "implicit",
        }
        assert prompt_cache.openai_prompt_cache_options(False, "explicit") == {
            "mode": "explicit", "ttl": "30m",
        }

    def test_is_bad_request(self):
        class Fake400(Exception):
            status_code = 400

        class Wrapped(Exception):
            pass

        assert prompt_cache.is_bad_request(Fake400("nope"))
        inner = Fake400("inner")
        wrapped = Wrapped("outer")
        wrapped.__cause__ = inner
        assert prompt_cache.is_bad_request(wrapped)
        assert not prompt_cache.is_bad_request(RuntimeError("timeout"))
