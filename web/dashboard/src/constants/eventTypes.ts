/**
 * WebSocket event type constants matching Go (pkg/events/types.go).
 */

// Persistent event types (stored in DB + NOTIFY)
export const EVENT_TIMELINE_CREATED = 'timeline_event.created' as const;
export const EVENT_TIMELINE_COMPLETED = 'timeline_event.completed' as const;
export const EVENT_SESSION_STATUS = 'session.status' as const;
export const EVENT_STAGE_STATUS = 'stage.status' as const;
export const EVENT_CHAT_CREATED = 'chat.created' as const;
export const EVENT_INTERACTION_CREATED = 'interaction.created' as const;

// Transient event types (NOTIFY only, no DB)
export const EVENT_STREAM_CHUNK = 'stream.chunk' as const;
export const EVENT_SESSION_PROGRESS = 'session.progress' as const;
export const EVENT_EXECUTION_PROGRESS = 'execution.progress' as const;

// Server → client control events
export const EVENT_CONNECTION_ESTABLISHED = 'connection.established' as const;
export const EVENT_CATCHUP_OVERFLOW = 'catchup.overflow' as const;
export const EVENT_PONG = 'pong' as const;

// Stage status values
export const STAGE_STATUS_STARTED = 'started' as const;
export const STAGE_STATUS_COMPLETED = 'completed' as const;
export const STAGE_STATUS_FAILED = 'failed' as const;
export const STAGE_STATUS_TIMED_OUT = 'timed_out' as const;
export const STAGE_STATUS_CANCELLED = 'cancelled' as const;

// Progress phase values
export const PROGRESS_PHASE_INVESTIGATING = 'investigating' as const;
export const PROGRESS_PHASE_GATHERING_INFO = 'gathering_info' as const;
export const PROGRESS_PHASE_DISTILLING = 'distilling' as const;
export const PROGRESS_PHASE_CONCLUDING = 'concluding' as const;
export const PROGRESS_PHASE_SYNTHESIZING = 'synthesizing' as const;
export const PROGRESS_PHASE_FINALIZING = 'finalizing' as const;

// Timeline event types (for routing to renderers)
export const TIMELINE_EVENT_TYPES = {
  LLM_THINKING: 'llm_thinking',
  NATIVE_THINKING: 'native_thinking',
  LLM_RESPONSE: 'llm_response',
  LLM_TOOL_CALL: 'llm_tool_call',
  MCP_TOOL_SUMMARY: 'mcp_tool_summary',
  FINAL_ANALYSIS: 'final_analysis',
  USER_QUESTION: 'user_question',
  EXECUTIVE_SUMMARY: 'executive_summary',
  CODE_EXECUTION: 'code_execution',
  GOOGLE_SEARCH_RESULT: 'google_search_result',
  URL_CONTEXT_RESULT: 'url_context_result',
  ERROR: 'error',
} as const;

// Timeline event statuses
export const TIMELINE_STATUS = {
  STREAMING: 'streaming',
  COMPLETED: 'completed',
  FAILED: 'failed',
  CANCELLED: 'cancelled',
  TIMED_OUT: 'timed_out',
} as const;
