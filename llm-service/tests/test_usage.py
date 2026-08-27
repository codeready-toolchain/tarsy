"""Tests for LangChain cache-token extraction (no input_tokens subtract)."""

import pytest

from llm.providers.usage import extract_cache_tokens

pytestmark = pytest.mark.unit


class TestExtractCacheTokens:
    def test_none_when_no_cache_keys(self):
        assert extract_cache_tokens({"input_tokens": 10, "output_tokens": 5}) is None
        assert extract_cache_tokens(None, None) is None

    def test_anthropic_raw_preferred_over_langchain_zero_create(self):
        # LangChain has reported cache_creation: 0 while raw create > 0.
        result = extract_cache_tokens(
            usage_metadata={
                "input_tokens": 100,
                "input_token_details": {"cache_read": 40, "cache_creation": 0},
            },
            response_metadata={
                "usage": {
                    "input_tokens": 20,
                    "cache_read_input_tokens": 40,
                    "cache_creation_input_tokens": 80,
                }
            },
        )
        assert result == (40, 80)

    def test_anthropic_ephemeral_split_when_create_missing(self):
        result = extract_cache_tokens(
            usage_metadata={
                "input_tokens": 100,
                "input_token_details": {
                    "cache_read": 10,
                    "ephemeral_5m_input_tokens": 15,
                    "ephemeral_1h_input_tokens": 25,
                },
            }
        )
        assert result == (10, 40)

    def test_anthropic_ephemeral_when_create_is_zero(self):
        result = extract_cache_tokens(
            usage_metadata={
                "input_token_details": {
                    "cache_read": 10,
                    "cache_creation": 0,
                    "ephemeral_5m_input_tokens": 7,
                    "ephemeral_1h_input_tokens": 3,
                }
            }
        )
        assert result == (10, 10)

    def test_openai_top_level_cached_and_write(self):
        result = extract_cache_tokens(
            usage_metadata={
                "input_tokens": 200,
                "cached_tokens": 50,
                "cache_write_tokens": 12,
            }
        )
        assert result == (50, 12)

    def test_openai_nested_prompt_tokens_details(self):
        result = extract_cache_tokens(
            usage_metadata={"input_tokens": 200},
            response_metadata={
                "usage": {
                    "prompt_tokens_details": {
                        "cached_tokens": 60,
                    },
                    "input_tokens_details": {
                        "cache_write_tokens": 8,
                    },
                }
            },
        )
        assert result == (60, 8)

    def test_openai_input_token_details(self):
        result = extract_cache_tokens(
            usage_metadata={
                "input_tokens": 200,
                "input_token_details": {
                    "cache_read": 30,
                    "cache_creation": 5,
                },
            }
        )
        assert result == (30, 5)

    def test_langchain_google_cache_read_only(self):
        result = extract_cache_tokens(
            usage_metadata={
                "input_tokens": 4000,
                "input_token_details": {"cache_read": 3500},
            }
        )
        assert result == (3500, 0)

    def test_raw_token_usage_alias(self):
        result = extract_cache_tokens(
            usage_metadata={"input_tokens": 1},
            response_metadata={"token_usage": {"cached_tokens": 9, "cache_write_tokens": 2}},
        )
        assert result == (9, 2)

    def test_explicit_zero_read_is_reported(self):
        result = extract_cache_tokens(
            usage_metadata={"input_token_details": {"cache_read": 0, "cache_creation": 4}}
        )
        assert result == (0, 4)
