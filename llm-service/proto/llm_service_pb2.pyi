from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class ThinkingRequest(_message.Message):
    __slots__ = ("session_id", "messages", "model", "temperature", "max_tokens")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    MESSAGES_FIELD_NUMBER: _ClassVar[int]
    MODEL_FIELD_NUMBER: _ClassVar[int]
    TEMPERATURE_FIELD_NUMBER: _ClassVar[int]
    MAX_TOKENS_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    messages: _containers.RepeatedCompositeFieldContainer[Message]
    model: str
    temperature: float
    max_tokens: int
    def __init__(self, session_id: _Optional[str] = ..., messages: _Optional[_Iterable[_Union[Message, _Mapping]]] = ..., model: _Optional[str] = ..., temperature: _Optional[float] = ..., max_tokens: _Optional[int] = ...) -> None: ...

class Message(_message.Message):
    __slots__ = ("role", "content")
    class Role(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
        __slots__ = ()
        ROLE_UNSPECIFIED: _ClassVar[Message.Role]
        ROLE_SYSTEM: _ClassVar[Message.Role]
        ROLE_USER: _ClassVar[Message.Role]
        ROLE_ASSISTANT: _ClassVar[Message.Role]
    ROLE_UNSPECIFIED: Message.Role
    ROLE_SYSTEM: Message.Role
    ROLE_USER: Message.Role
    ROLE_ASSISTANT: Message.Role
    ROLE_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    role: Message.Role
    content: str
    def __init__(self, role: _Optional[_Union[Message.Role, str]] = ..., content: _Optional[str] = ...) -> None: ...

class ThinkingChunk(_message.Message):
    __slots__ = ("thinking", "response", "error")
    THINKING_FIELD_NUMBER: _ClassVar[int]
    RESPONSE_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    thinking: ThinkingContent
    response: ResponseContent
    error: ErrorContent
    def __init__(self, thinking: _Optional[_Union[ThinkingContent, _Mapping]] = ..., response: _Optional[_Union[ResponseContent, _Mapping]] = ..., error: _Optional[_Union[ErrorContent, _Mapping]] = ...) -> None: ...

class ThinkingContent(_message.Message):
    __slots__ = ("content", "is_complete")
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    IS_COMPLETE_FIELD_NUMBER: _ClassVar[int]
    content: str
    is_complete: bool
    def __init__(self, content: _Optional[str] = ..., is_complete: bool = ...) -> None: ...

class ResponseContent(_message.Message):
    __slots__ = ("content", "is_complete", "is_final")
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    IS_COMPLETE_FIELD_NUMBER: _ClassVar[int]
    IS_FINAL_FIELD_NUMBER: _ClassVar[int]
    content: str
    is_complete: bool
    is_final: bool
    def __init__(self, content: _Optional[str] = ..., is_complete: bool = ..., is_final: bool = ...) -> None: ...

class ErrorContent(_message.Message):
    __slots__ = ("message", "retryable")
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    RETRYABLE_FIELD_NUMBER: _ClassVar[int]
    message: str
    retryable: bool
    def __init__(self, message: _Optional[str] = ..., retryable: bool = ...) -> None: ...
