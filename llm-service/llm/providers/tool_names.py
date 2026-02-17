"""Shared tool name encoding between canonical and LLM API formats.

All LLM providers use the same encoding:
  canonical:  server.tool       (dot-separated, used by Go backend)
  api:        server__tool      (double-underscore, used by LLM APIs)

The mapping uses '__' as the dot replacement. Tool name segments
(the parts between dots) must not contain '__' themselves, or the
round-trip would be lossy.
"""


def tool_name_to_api(name: str) -> str:
    """Convert canonical 'server.tool' to 'server__tool' for LLM APIs.

    Raises ValueError if any segment contains '__', which would make
    the round-trip lossy.
    """
    for segment in name.split("."):
        if "__" in segment:
            raise ValueError(
                f"Tool name segment '{segment}' in '{name}' contains '__' "
                f"which conflicts with the dot separator encoding. "
                f"Rename the tool to avoid double underscores."
            )
    return name.replace(".", "__")


def tool_name_from_api(name: str) -> str:
    """Convert 'server__tool' back to canonical 'server.tool'."""
    return name.replace("__", ".")
