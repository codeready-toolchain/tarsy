"""Tests for HTTP status extraction used by provider retries."""

import pytest

from llm.providers import http_status

pytestmark = pytest.mark.unit


def _exc(*, message="err", status_code=None, code=None, status=None, response_status=None):
    err = Exception(message)
    if status_code is not None:
        err.status_code = status_code
    if code is not None:
        err.code = code
    if status is not None:
        err.status = status
    if response_status is not None:
        err.response = type("Resp", (), {"status_code": response_status})()
    return err


class TestExtractHttpStatus:
    @pytest.mark.parametrize(
        "err,expected",
        [
            (_exc(status_code=404), 404),
            (_exc(code=404, status="NOT_FOUND"), 404),
            (_exc(message="Error code: 404 - [{'error': {'message': 'not found'}}]"), 404),
            (_exc(status_code=429), 429),
            (_exc(status_code=503), 503),
            (_exc(status_code=400), 400),
            (_exc(status_code=401), 401),
            (_exc(status_code=403), 403),
            (_exc(response_status=404), 404),
            (_exc(code=5, message="Error code: 404 - [{'error': ...}]"), 404),
            (_exc(status="NOT_FOUND"), None),
            (RuntimeError("timeout"), None),
        ],
        ids=[
            "status_code_404",
            "code_attr_404",
            "message_only_404",
            "status_code_429",
            "status_code_503",
            "status_code_400",
            "status_code_401",
            "status_code_403",
            "nested_response",
            "skip_grpc_code",
            "string_not_found",
            "plain_runtime",
        ],
    )
    def test_extract(self, err, expected):
        assert http_status.extract_http_status(err) == expected

    def test_walks_cause(self):
        inner = _exc(status_code=404)
        wrapped = RuntimeError("outer")
        wrapped.__cause__ = inner
        assert http_status.extract_http_status(wrapped) == 404

    @pytest.mark.parametrize("status", [404, 429, 503])
    def test_walks_context_when_cause_has_no_status(self, status):
        wrapped = RuntimeError("outer")
        wrapped.__cause__ = RuntimeError("unclassified")
        wrapped.__context__ = _exc(status_code=status)
        assert http_status.extract_http_status(wrapped) == status
        assert http_status.is_retryable_http(wrapped)


class TestIsRetryableHttp:
    @pytest.mark.parametrize(
        "err,expected",
        [
            (_exc(status_code=404), True),
            (_exc(code=404, status="NOT_FOUND"), True),
            (_exc(message="Error code: 404 - [{'error': ...}]"), True),
            (_exc(status_code=429), True),
            (_exc(status_code=503), True),
            (_exc(status_code=500), True),
            (_exc(status_code=400), False),
            (_exc(status_code=401), False),
            (_exc(status_code=403), False),
            (_exc(status="NOT_FOUND"), False),
            (RuntimeError("timeout"), False),
        ],
    )
    def test_retryable(self, err, expected):
        assert http_status.is_retryable_http(err) is expected

    def test_wrapped_404_is_retryable(self):
        inner = _exc(message="Error code: 404 - [{'error': ...}]")
        wrapped = RuntimeError("outer")
        wrapped.__cause__ = inner
        assert http_status.is_retryable_http(wrapped)
