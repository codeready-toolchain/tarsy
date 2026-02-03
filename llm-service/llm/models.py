"""Minimal models for LLM service - copied from tarsy-bot."""
from enum import Enum
from typing import List
from pydantic import BaseModel, Field


class MessageRole(str, Enum):
    """Supported LLM message roles."""
    SYSTEM = "system"
    USER = "user"
    ASSISTANT = "assistant"


class LLMMessage(BaseModel):
    """Individual message in LLM conversation."""
    role: MessageRole
    content: str = Field(..., min_length=1)


class LLMConversation(BaseModel):
    """Complete conversation thread."""
    messages: List[LLMMessage] = Field(..., min_length=1)
    
    def append_assistant_message(self, content: str) -> None:
        """Add assistant message to conversation."""
        self.messages.append(LLMMessage(role=MessageRole.ASSISTANT, content=content))
