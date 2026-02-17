"""Tests for shared tool name encoding utility."""
import pytest

from llm.providers.tool_names import tool_name_to_api, tool_name_from_api

pytestmark = pytest.mark.unit


class TestToolNameToApi:
    """Test tool_name_to_api conversion."""

    def test_single_dot(self):
        assert tool_name_to_api("server.tool") == "server__tool"

    def test_multiple_dots(self):
        assert tool_name_to_api("my.server.tool") == "my__server__tool"

    def test_no_dots(self):
        assert tool_name_to_api("notool") == "notool"

    def test_empty_string(self):
        assert tool_name_to_api("") == ""

    def test_rejects_double_underscore_in_segment(self):
        with pytest.raises(ValueError, match="contains '__'"):
            tool_name_to_api("server.my__helper")

    def test_rejects_double_underscore_in_first_segment(self):
        with pytest.raises(ValueError, match="contains '__'"):
            tool_name_to_api("my__server.tool")

    def test_rejects_double_underscore_no_dots(self):
        with pytest.raises(ValueError, match="contains '__'"):
            tool_name_to_api("my__tool")


class TestToolNameFromApi:
    """Test tool_name_from_api conversion."""

    def test_single_separator(self):
        assert tool_name_from_api("server__tool") == "server.tool"

    def test_multiple_separators(self):
        assert tool_name_from_api("my__server__tool") == "my.server.tool"

    def test_no_separator(self):
        assert tool_name_from_api("notool") == "notool"

    def test_empty_string(self):
        assert tool_name_from_api("") == ""


class TestRoundTrip:
    """Test round-trip consistency."""

    def test_round_trip_single_dot(self):
        name = "server.tool"
        assert tool_name_from_api(tool_name_to_api(name)) == name

    def test_round_trip_multiple_dots(self):
        name = "my.server.tool"
        assert tool_name_from_api(tool_name_to_api(name)) == name

    def test_round_trip_no_dots(self):
        name = "notool"
        assert tool_name_from_api(tool_name_to_api(name)) == name
