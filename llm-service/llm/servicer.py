"""gRPC servicer implementation for LLM service."""
from typing import AsyncIterator

import grpc

from proto import llm_service_pb2 as pb
from proto import llm_service_pb2_grpc as pb_grpc
from llm.gemini_client import GeminiNativeThinkingClient
from llm.models import LLMConversation, LLMMessage, MessageRole


class LLMServicer(pb_grpc.LLMServiceServicer):
    """gRPC servicer for LLM operations."""
    
    def __init__(self, api_key: str, model: str, temperature: float = 1.0):
        """Initialize the servicer with Gemini client."""
        self.client = GeminiNativeThinkingClient(
            api_key=api_key,
            model=model,
            temperature=temperature
        )
        print(f"LLM Servicer initialized with model {model}")
    
    def _pb_to_conversation(self, request: pb.ThinkingRequest) -> LLMConversation:
        """Convert protobuf request to internal conversation model."""
        messages = []
        
        for msg in request.messages:
            # Map protobuf roles to internal MessageRole
            role_map = {
                pb.Message.ROLE_SYSTEM: MessageRole.SYSTEM,
                pb.Message.ROLE_USER: MessageRole.USER,
                pb.Message.ROLE_ASSISTANT: MessageRole.ASSISTANT,
            }
            role = role_map.get(msg.role, MessageRole.USER)
            messages.append(LLMMessage(role=role, content=msg.content))
        
        return LLMConversation(messages=messages)
    
    async def GenerateWithThinking(
        self,
        request: pb.ThinkingRequest,
        context: grpc.aio.ServicerContext
    ) -> AsyncIterator[pb.ThinkingChunk]:
        """Generate streaming response with native thinking."""
        print(f"Received GenerateWithThinking request for session {request.session_id}")
        
        try:
            # Convert request to conversation
            conversation = self._pb_to_conversation(request)
            
            # Get configuration from request
            max_tokens = request.max_tokens if request.HasField('max_tokens') else None
            
            # Stream responses
            async for chunk in self.client.generate_stream(
                conversation=conversation,
                session_id=request.session_id,
                max_tokens=max_tokens
            ):
                # Convert to protobuf chunk
                if chunk.is_thinking:
                    yield pb.ThinkingChunk(
                        thinking=pb.ThinkingContent(
                            content=chunk.content,
                            is_complete=chunk.is_complete
                        )
                    )
                else:
                    yield pb.ThinkingChunk(
                        response=pb.ResponseContent(
                            content=chunk.content,
                            is_complete=chunk.is_complete,
                            is_final=chunk.is_final
                        )
                    )
            
            print(f"Completed GenerateWithThinking for session {request.session_id}")
            
        except TimeoutError as e:
            print(f"Timeout error for session {request.session_id}: {e}")
            yield pb.ThinkingChunk(
                error=pb.ErrorContent(
                    message=str(e),
                    retryable=True
                )
            )
            
        except Exception as e:
            print(f"Error in GenerateWithThinking for session {request.session_id}: {e}")
            yield pb.ThinkingChunk(
                error=pb.ErrorContent(
                    message=f"Generation failed: {str(e)}",
                    retryable=False
                )
            )
