"""gRPC servicer implementation for LLM service."""
import os
from typing import AsyncIterator

import grpc

from proto import llm_service_pb2 as pb
from proto import llm_service_pb2_grpc as pb_grpc
from llm.gemini_client import GeminiNativeThinkingClient
from llm.models import LLMConversation, LLMMessage, MessageRole


class LLMServicer(pb_grpc.LLMServiceServicer):
    """gRPC servicer for LLM operations."""
    
    def __init__(self):
        """Initialize the servicer."""
        print("LLM Servicer initialized - will resolve credentials per-request")
    
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
    
    def _resolve_credentials(self, llm_config: pb.LLMConfig) -> str:
        """Resolve API key from environment variables specified in config."""
        # Resolve primary API key from env var name
        if llm_config.api_key_env:
            api_key = os.getenv(llm_config.api_key_env)
            if not api_key:
                raise ValueError(
                    f"Environment variable '{llm_config.api_key_env}' not found. "
                    f"Please set it for provider '{llm_config.provider}'."
                )
            return api_key
        
        # For VertexAI, credentials are resolved via credentials_env
        if llm_config.credentials_env:
            creds_path = os.getenv(llm_config.credentials_env)
            if not creds_path:
                raise ValueError(
                    f"Environment variable '{llm_config.credentials_env}' not found. "
                    f"Please set it for provider '{llm_config.provider}'."
                )
            # VertexAI uses application default credentials - return empty string
            return ""
        
        raise ValueError(
            f"No credentials configured for provider '{llm_config.provider}'. "
            f"Please specify api_key_env or credentials_env in LLMConfig."
        )
    
    async def GenerateWithThinking(
        self,
        request: pb.ThinkingRequest,
        context: grpc.aio.ServicerContext
    ) -> AsyncIterator[pb.ThinkingChunk]:
        """Generate streaming response with native thinking."""
        print(f"Received GenerateWithThinking request for session {request.session_id}")
        
        try:
            # Validate llm_config is provided
            if not request.HasField('llm_config'):
                raise ValueError("llm_config is required in ThinkingRequest")
            
            llm_config = request.llm_config
            
            # Resolve credentials from environment
            api_key = self._resolve_credentials(llm_config)
            
            # Create client with resolved credentials
            # TODO: Support other providers (OpenAI, Anthropic, XAI, VertexAI)
            if llm_config.provider != "google":
                raise ValueError(f"Unsupported provider: {llm_config.provider}. Only 'google' is currently supported.")
            
            client = GeminiNativeThinkingClient(
                api_key=api_key,
                model=llm_config.model or "gemini-2.0-flash-thinking-exp-01-21",
                temperature=1.0  # TODO: Support temperature in LLMConfig
            )
            
            # Convert request to conversation
            conversation = self._pb_to_conversation(request)
            
            # Get configuration from request
            max_tokens = request.max_tokens if request.HasField('max_tokens') else None
            
            # Stream responses
            async for chunk in client.generate_stream(
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
            
        except ValueError as e:
            print(f"Configuration error for session {request.session_id}: {e}")
            yield pb.ThinkingChunk(
                error=pb.ErrorContent(
                    message=str(e),
                    retryable=False
                )
            )
            
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
