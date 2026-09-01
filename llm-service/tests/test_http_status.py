"""Tests for HTTP status extraction used by provider retries."""

import errno
import ssl
from datetime import datetime, timedelta, timezone
from email.utils import format_datetime

import httpx
import pytest

from llm.providers import http_status

pytestmark = pytest.mark.unit


def _exc(
    *,
    message="err",
    status_code=None,
    code=None,
    status=None,
    response_status=None,
    headers=None,
    details=None,
):
    err = Exception(message)
    if status_code is not None:
        err.status_code = status_code
    if code is not None:
        err.code = code
    if status is not None:
        err.status = status
    if response_status is not None or headers is not None:
        err.response = type("Resp", (), {
            "status_code": response_status if response_status is not None else status_code,
            "headers": headers or {},
        })()
    if details is not None:
        err.details = details
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
        assert http_status.is_retryable(wrapped)


class TestIsRetryable:
    @pytest.mark.parametrize(
        "err,expected",
        [
            (_exc(status_code=404), True),
            (_exc(code=404, status="NOT_FOUND"), True),
            (_exc(message="Error code: 404 - [{'error': ...}]"), True),
            (_exc(status_code=429), True),
            (_exc(status_code=408), True),
            (_exc(status_code=503), True),
            (_exc(status_code=500), True),
            (_exc(status_code=529), True),
            (_exc(status_code=400), False),
            (_exc(status_code=401), False),
            (_exc(status_code=403), False),
            (_exc(status_code=409), False),
            (_exc(status="NOT_FOUND"), False),
            (RuntimeError("timeout"), False),
            (httpx.ConnectError("connection failed"), True),
            (httpx.RemoteProtocolError("server disconnected"), True),
            (OSError(errno.ECONNRESET, "Connection reset by peer"), True),
            (ssl.SSLCertVerificationError("CERTIFICATE_VERIFY_FAILED"), False),
            (_exc(status_code=429, message="usage_limit_reached"), False),
            (_exc(status_code=429, headers={"x-should-retry": "false"}), False),
            (_exc(status_code=429, headers={"Retry-After": "3600"}), False),
        ],
    )
    def test_retryable(self, err, expected):
        assert http_status.is_retryable(err) is expected

    def test_wrapped_404_is_retryable(self):
        inner = _exc(message="Error code: 404 - [{'error': ...}]")
        wrapped = RuntimeError("outer")
        wrapped.__cause__ = inner
        assert http_status.is_retryable(wrapped)

    def test_wrapped_connect_error_is_retryable(self):
        wrapped = RuntimeError("outer")
        wrapped.__cause__ = httpx.ConnectError("connection failed")
        assert http_status.is_retryable(wrapped)

    def test_tls_inside_connect_error_is_not_retryable(self):
        wrapped = httpx.ConnectError("TLS handshake failed")
        wrapped.__cause__ = ssl.SSLCertVerificationError("CERTIFICATE_VERIFY_FAILED")
        assert http_status.is_retryable(wrapped) is False


class TestExtractRetryHint:
    def test_retry_after_seconds(self):
        err = _exc(status_code=429, headers={"Retry-After": "53"})
        assert http_status.extract_retry_hint(err) == 53.0

    def test_retry_after_ms(self):
        err = _exc(status_code=429, headers={"retry-after-ms": "53000"})
        assert http_status.extract_retry_hint(err) == 53.0

    def test_retry_info_delay(self):
        err = _exc(
            status_code=429,
            details={
                "details": [
                    {
                        "@type": "type.googleapis.com/google.rpc.RetryInfo",
                        "retryDelay": "53s",
                    },
                ],
            },
        )
        assert http_status.extract_retry_hint(err) == 53.0

    def test_http_date_in_the_past_is_no_hint(self):
        past = format_datetime(datetime.now(timezone.utc) - timedelta(hours=1))
        err = _exc(status_code=429, headers={"Retry-After": past})
        assert http_status.extract_retry_hint(err) is None
