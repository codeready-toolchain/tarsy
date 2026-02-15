/**
 * WebSocket event payload types derived from Go (pkg/events/payloads.go).
 */

/** timeline_event.created payload. */
export interface TimelineCreatedPayload {
  type: 'timeline_event.created';
  event_id: string;
  session_id: string;
  stage_id?: string;
  execution_id?: string;
  event_type: string;
  status: string;
  content: string;
  metadata?: Record<string, unknown>;
  sequence_number: number;
  timestamp: string;
}

/** timeline_event.completed payload. */
export interface TimelineCompletedPayload {
  type: 'timeline_event.completed';
  session_id: string;
  event_id: string;
  event_type: string;
  content: string;
  status: string;
  metadata?: Record<string, unknown>;
  timestamp: string;
}

/** stream.chunk transient payload. */
export interface StreamChunkPayload {
  type: 'stream.chunk';
  session_id: string;
  event_id: string;
  delta: string;
  timestamp: string;
}

/** session.status payload. */
export interface SessionStatusPayload {
  type: 'session.status';
  session_id: string;
  status: string;
  timestamp: string;
}

/** stage.status payload. */
export interface StageStatusPayload {
  type: 'stage.status';
  session_id: string;
  stage_id?: string;
  stage_name: string;
  stage_index: number;
  status: string;
  timestamp: string;
}

/** chat.created payload. */
export interface ChatCreatedPayload {
  type: 'chat.created';
  session_id: string;
  chat_id: string;
  created_by: string;
  timestamp: string;
}

/** interaction.created payload (for trace view live updates). */
export interface InteractionCreatedPayload {
  type: 'interaction.created';
  session_id: string;
  stage_id?: string;
  execution_id?: string;
  interaction_id: string;
  interaction_type: 'llm' | 'mcp';
  timestamp: string;
}

/** session.progress transient payload (for active alerts panel). */
export interface SessionProgressPayload {
  type: 'session.progress';
  session_id: string;
  current_stage_name: string;
  current_stage_index: number;
  total_stages: number;
  active_executions: number;
  status_text: string;
  timestamp: string;
}

/** execution.progress transient payload (for per-agent progress). */
export interface ExecutionProgressPayload {
  type: 'execution.progress';
  session_id: string;
  stage_id: string;
  execution_id: string;
  phase: string;
  message: string;
  timestamp: string;
}

/** execution.status transient payload (for per-agent status transitions). */
export interface ExecutionStatusPayload {
  type: 'execution.status';
  session_id: string;
  stage_id: string;
  execution_id: string;
  status: string;
  error_message?: string;
  timestamp: string;
}

/** Union of all possible WebSocket event payloads. */
export type WebSocketEvent =
  | TimelineCreatedPayload
  | TimelineCompletedPayload
  | StreamChunkPayload
  | SessionStatusPayload
  | StageStatusPayload
  | ChatCreatedPayload
  | InteractionCreatedPayload
  | SessionProgressPayload
  | ExecutionProgressPayload
  | ExecutionStatusPayload;
