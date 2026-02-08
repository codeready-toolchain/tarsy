from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class GenerateRequest(_message.Message):
    __slots__ = ("session_id", "messages", "llm_config", "tools", "execution_id")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    MESSAGES_FIELD_NUMBER: _ClassVar[int]
    LLM_CONFIG_FIELD_NUMBER: _ClassVar[int]
    TOOLS_FIELD_NUMBER: _ClassVar[int]
    EXECUTION_ID_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    messages: _containers.RepeatedCompositeFieldContainer[ConversationMessage]
    llm_config: LLMConfig
    tools: _containers.RepeatedCompositeFieldContainer[ToolDefinition]
    execution_id: str
    def __init__(self, session_id: _Optional[str] = ..., messages: _Optional[_Iterable[_Union[ConversationMessage, _Mapping]]] = ..., llm_config: _Optional[_Union[LLMConfig, _Mapping]] = ..., tools: _Optional[_Iterable[_Union[ToolDefinition, _Mapping]]] = ..., execution_id: _Optional[str] = ...) -> None: ...

class GenerateResponse(_message.Message):
    __slots__ = ("text", "thinking", "tool_call", "usage", "error", "code_execution", "is_final")
    TEXT_FIELD_NUMBER: _ClassVar[int]
    THINKING_FIELD_NUMBER: _ClassVar[int]
    TOOL_CALL_FIELD_NUMBER: _ClassVar[int]
    USAGE_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    CODE_EXECUTION_FIELD_NUMBER: _ClassVar[int]
    IS_FINAL_FIELD_NUMBER: _ClassVar[int]
    text: TextDelta
    thinking: ThinkingDelta
    tool_call: ToolCallDelta
    usage: UsageInfo
    error: ErrorInfo
    code_execution: CodeExecutionDelta
    is_final: bool
    def __init__(self, text: _Optional[_Union[TextDelta, _Mapping]] = ..., thinking: _Optional[_Union[ThinkingDelta, _Mapping]] = ..., tool_call: _Optional[_Union[ToolCallDelta, _Mapping]] = ..., usage: _Optional[_Union[UsageInfo, _Mapping]] = ..., error: _Optional[_Union[ErrorInfo, _Mapping]] = ..., code_execution: _Optional[_Union[CodeExecutionDelta, _Mapping]] = ..., is_final: bool = ...) -> None: ...

class ConversationMessage(_message.Message):
    __slots__ = ("role", "content", "tool_calls", "tool_call_id", "tool_name")
    ROLE_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    TOOL_CALLS_FIELD_NUMBER: _ClassVar[int]
    TOOL_CALL_ID_FIELD_NUMBER: _ClassVar[int]
    TOOL_NAME_FIELD_NUMBER: _ClassVar[int]
    role: str
    content: str
    tool_calls: _containers.RepeatedCompositeFieldContainer[ToolCall]
    tool_call_id: str
    tool_name: str
    def __init__(self, role: _Optional[str] = ..., content: _Optional[str] = ..., tool_calls: _Optional[_Iterable[_Union[ToolCall, _Mapping]]] = ..., tool_call_id: _Optional[str] = ..., tool_name: _Optional[str] = ...) -> None: ...

class ToolDefinition(_message.Message):
    __slots__ = ("name", "description", "parameters_schema")
    NAME_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    PARAMETERS_SCHEMA_FIELD_NUMBER: _ClassVar[int]
    name: str
    description: str
    parameters_schema: str
    def __init__(self, name: _Optional[str] = ..., description: _Optional[str] = ..., parameters_schema: _Optional[str] = ...) -> None: ...

class ToolCall(_message.Message):
    __slots__ = ("id", "name", "arguments")
    ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    ARGUMENTS_FIELD_NUMBER: _ClassVar[int]
    id: str
    name: str
    arguments: str
    def __init__(self, id: _Optional[str] = ..., name: _Optional[str] = ..., arguments: _Optional[str] = ...) -> None: ...

class TextDelta(_message.Message):
    __slots__ = ("content",)
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    content: str
    def __init__(self, content: _Optional[str] = ...) -> None: ...

class ThinkingDelta(_message.Message):
    __slots__ = ("content",)
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    content: str
    def __init__(self, content: _Optional[str] = ...) -> None: ...

class ToolCallDelta(_message.Message):
    __slots__ = ("call_id", "name", "arguments")
    CALL_ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    ARGUMENTS_FIELD_NUMBER: _ClassVar[int]
    call_id: str
    name: str
    arguments: str
    def __init__(self, call_id: _Optional[str] = ..., name: _Optional[str] = ..., arguments: _Optional[str] = ...) -> None: ...

class CodeExecutionDelta(_message.Message):
    __slots__ = ("code", "result")
    CODE_FIELD_NUMBER: _ClassVar[int]
    RESULT_FIELD_NUMBER: _ClassVar[int]
    code: str
    result: str
    def __init__(self, code: _Optional[str] = ..., result: _Optional[str] = ...) -> None: ...

class UsageInfo(_message.Message):
    __slots__ = ("input_tokens", "output_tokens", "total_tokens", "thinking_tokens")
    INPUT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    OUTPUT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    TOTAL_TOKENS_FIELD_NUMBER: _ClassVar[int]
    THINKING_TOKENS_FIELD_NUMBER: _ClassVar[int]
    input_tokens: int
    output_tokens: int
    total_tokens: int
    thinking_tokens: int
    def __init__(self, input_tokens: _Optional[int] = ..., output_tokens: _Optional[int] = ..., total_tokens: _Optional[int] = ..., thinking_tokens: _Optional[int] = ...) -> None: ...

class ErrorInfo(_message.Message):
    __slots__ = ("message", "code", "retryable")
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    CODE_FIELD_NUMBER: _ClassVar[int]
    RETRYABLE_FIELD_NUMBER: _ClassVar[int]
    message: str
    code: str
    retryable: bool
    def __init__(self, message: _Optional[str] = ..., code: _Optional[str] = ..., retryable: bool = ...) -> None: ...

class LLMConfig(_message.Message):
    __slots__ = ("provider", "model", "api_key_env", "credentials_env", "base_url", "max_tool_result_tokens", "native_tools", "project", "location", "backend")
    class NativeToolsEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: bool
        def __init__(self, key: _Optional[str] = ..., value: bool = ...) -> None: ...
    PROVIDER_FIELD_NUMBER: _ClassVar[int]
    MODEL_FIELD_NUMBER: _ClassVar[int]
    API_KEY_ENV_FIELD_NUMBER: _ClassVar[int]
    CREDENTIALS_ENV_FIELD_NUMBER: _ClassVar[int]
    BASE_URL_FIELD_NUMBER: _ClassVar[int]
    MAX_TOOL_RESULT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    NATIVE_TOOLS_FIELD_NUMBER: _ClassVar[int]
    PROJECT_FIELD_NUMBER: _ClassVar[int]
    LOCATION_FIELD_NUMBER: _ClassVar[int]
    BACKEND_FIELD_NUMBER: _ClassVar[int]
    provider: str
    model: str
    api_key_env: str
    credentials_env: str
    base_url: str
    max_tool_result_tokens: int
    native_tools: _containers.ScalarMap[str, bool]
    project: str
    location: str
    backend: str
    def __init__(self, provider: _Optional[str] = ..., model: _Optional[str] = ..., api_key_env: _Optional[str] = ..., credentials_env: _Optional[str] = ..., base_url: _Optional[str] = ..., max_tool_result_tokens: _Optional[int] = ..., native_tools: _Optional[_Mapping[str, bool]] = ..., project: _Optional[str] = ..., location: _Optional[str] = ..., backend: _Optional[str] = ...) -> None: ...
