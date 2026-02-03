"""
Simplified Gemini Client for PoC - Native Thinking Support.

Stripped down version from tarsy-bot focusing only on:
- Native thinking with ThinkingConfig
- Streaming support
- No database hooks, no MCP tools, no native tools
"""
import asyncio
import uuid
from dataclasses import dataclass
from typing import AsyncIterator, List, Optional

from google import genai
from google.genai import types as google_genai_types

from llm.models import LLMConversation, MessageRole


# Streaming chunk sizes
THINKING_CHUNK_SIZE = 1
RESPONSE_CHUNK_SIZE = 2


@dataclass
class StreamChunk:
    """Streaming chunk from Gemini."""
    content: str
    is_thinking: bool
    is_complete: bool
    is_final: bool = False  # No more tool calls expected


@dataclass
class GeminiResponse:
    """Response from Gemini generation."""
    content: str
    thinking_content: Optional[str] = None
    conversation: Optional[LLMConversation] = None


class GeminiNativeThinkingClient:
    """
    Simplified Gemini client for PoC.
    
    Supports native thinking streaming without the complexity of
    the full tarsy-bot client (no hooks, no MCP, no native tools).
    """
    
    def __init__(self, api_key: str, model: str = "gemini-2.0-flash-thinking-exp-01-21", temperature: float = 1.0):
        """Initialize the client."""
        self.api_key = api_key
        self.model = model
        self.temperature = temperature
        self.client = genai.Client(api_key=api_key)
        print(f"Initialized Gemini client with model {model}")
    
    def _get_thinking_config(self) -> google_genai_types.ThinkingConfig:
        """Get thinking configuration based on model."""
        if "gemini-2.5-pro" in self.model.lower():
            return google_genai_types.ThinkingConfig(
                thinking_budget=32768,
                include_thoughts=True
            )
        elif "gemini-2.5-flash" in self.model.lower():
            return google_genai_types.ThinkingConfig(
                thinking_budget=24576,
                include_thoughts=True
            )
        else:
            # Default for 3 pro-preview
            return google_genai_types.ThinkingConfig(
                thinking_level=google_genai_types.ThinkingLevel.HIGH,
                include_thoughts=True
            )
    
    def _convert_to_native_format(self, conversation: LLMConversation) -> List[google_genai_types.Content]:
        """Convert conversation to Gemini native format."""
        contents = []
        
        for msg in conversation.messages:
            if msg.role == MessageRole.SYSTEM:
                contents.append(google_genai_types.Content(
                    role="user",
                    parts=[google_genai_types.Part(text=f"[System Instructions]\n{msg.content}")]
                ))
            elif msg.role == MessageRole.USER:
                contents.append(google_genai_types.Content(
                    role="user",
                    parts=[google_genai_types.Part(text=msg.content)]
                ))
            elif msg.role == MessageRole.ASSISTANT:
                contents.append(google_genai_types.Content(
                    role="model",
                    parts=[google_genai_types.Part(text=msg.content)]
                ))
        
        return contents
    
    async def generate_stream(
        self,
        conversation: LLMConversation,
        session_id: str,
        max_tokens: Optional[int] = None,
        timeout_seconds: int = 180
    ) -> AsyncIterator[StreamChunk]:
        """
        Generate response with streaming.
        
        Yields StreamChunk objects containing thinking and response content.
        """
        request_id = str(uuid.uuid4())[:8]
        print(f"[{request_id}] Starting generation for session {session_id}")
        
        # Convert conversation
        contents = self._convert_to_native_format(conversation)
        
        # Configure thinking
        thinking_config = self._get_thinking_config()
        
        # Build generation config
        gen_config = google_genai_types.GenerateContentConfig(
            temperature=self.temperature,
            max_output_tokens=max_tokens,
            thinking_config=thinking_config
        )
        
        # Track streaming state
        accumulated_thinking = ""
        accumulated_response = ""
        thinking_token_count = 0
        response_token_count = 0
        is_streaming_thinking = False
        is_streaming_response = False
        
        try:
            async with asyncio.timeout(timeout_seconds):
                async for chunk in await self.client.aio.models.generate_content_stream(
                    model=self.model,
                    contents=contents,
                    config=gen_config
                ):
                    if chunk.candidates and len(chunk.candidates) > 0:
                        candidate = chunk.candidates[0]
                        
                        if candidate.content and candidate.content.parts:
                            for part in candidate.content.parts:
                                # Check for thinking content
                                if hasattr(part, 'thought') and part.thought:
                                    if hasattr(part, 'text') and part.text:
                                        accumulated_thinking += part.text
                                        thinking_token_count += 1
                                        
                                        if not is_streaming_thinking:
                                            is_streaming_thinking = True
                                            print(f"[{request_id}] Started streaming thinking")
                                        
                                        # Yield thinking chunks
                                        if thinking_token_count >= THINKING_CHUNK_SIZE:
                                            yield StreamChunk(
                                                content=accumulated_thinking,
                                                is_thinking=True,
                                                is_complete=False
                                            )
                                            thinking_token_count = 0
                                
                                elif hasattr(part, 'text') and part.text:
                                    # Regular response text
                                    accumulated_response += part.text
                                    response_token_count += 1
                                    
                                    # Send final thinking chunk if transitioning
                                    if is_streaming_thinking:
                                        yield StreamChunk(
                                            content=accumulated_thinking,
                                            is_thinking=True,
                                            is_complete=True
                                        )
                                        is_streaming_thinking = False
                                        print(f"[{request_id}] Completed streaming thinking")
                                    
                                    if not is_streaming_response:
                                        is_streaming_response = True
                                        print(f"[{request_id}] Started streaming response")
                                    
                                    # Yield response chunks
                                    if response_token_count >= RESPONSE_CHUNK_SIZE:
                                        yield StreamChunk(
                                            content=accumulated_response,
                                            is_thinking=False,
                                            is_complete=False
                                        )
                                        response_token_count = 0
                
                # Send final chunks
                if is_streaming_thinking and accumulated_thinking:
                    yield StreamChunk(
                        content=accumulated_thinking,
                        is_thinking=True,
                        is_complete=True
                    )
                
                if is_streaming_response and accumulated_response:
                    yield StreamChunk(
                        content=accumulated_response,
                        is_thinking=False,
                        is_complete=True,
                        is_final=True
                    )
                
                print(f"[{request_id}] Generation complete")
                
        except asyncio.TimeoutError:
            print(f"[{request_id}] Generation timed out after {timeout_seconds}s")
            raise TimeoutError(f"Generation timed out after {timeout_seconds}s")
        except Exception as e:
            print(f"[{request_id}] Generation failed: {e}")
            raise
