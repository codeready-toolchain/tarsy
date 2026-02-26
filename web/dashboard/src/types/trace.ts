/**
 * Trace/observability types derived from Go models (pkg/models/interaction.go).
 */

/** Top-level trace response. */
export interface TraceListResponse {
  stages: TraceStageGroup[];
  session_interactions: LLMInteractionListItem[];
}

/** Stage group containing executions. */
export interface TraceStageGroup {
  stage_id: string;
  stage_name: string;
  executions: TraceExecutionGroup[];
}

/** Execution group containing interactions. */
export interface TraceExecutionGroup {
  execution_id: string;
  agent_name: string;
  llm_interactions: LLMInteractionListItem[];
  mcp_interactions: MCPInteractionListItem[];
  sub_agents?: TraceExecutionGroup[];
}

/** LLM interaction list item (collapsed view). */
export interface LLMInteractionListItem {
  id: string;
  interaction_type: string;
  model_name: string;
  input_tokens?: number;
  output_tokens?: number;
  total_tokens?: number;
  duration_ms?: number;
  error_message?: string;
  created_at: string;
}

/** MCP interaction list item (collapsed view). */
export interface MCPInteractionListItem {
  id: string;
  interaction_type: string;
  server_name: string;
  tool_name?: string;
  duration_ms?: number;
  error_message?: string;
  created_at: string;
}

/** Full LLM interaction detail. */
export interface LLMInteractionDetailResponse {
  id: string;
  interaction_type: string;
  model_name: string;
  thinking_content?: string;
  input_tokens?: number;
  output_tokens?: number;
  total_tokens?: number;
  duration_ms?: number;
  error_message?: string;
  llm_request: Record<string, unknown>;
  llm_response: Record<string, unknown>;
  response_metadata?: Record<string, unknown>;
  created_at: string;
  conversation: ConversationMessage[];
}

/** Single message in reconstructed conversation. */
export interface ConversationMessage {
  role: string;
  content: string;
  tool_calls?: MessageToolCall[];
  tool_call_id?: string;
  tool_name?: string;
}

/** Tool call within a message. */
export interface MessageToolCall {
  id: string;
  name: string;
  arguments: string;
}

/** Tool entry in a tool_list interaction. */
export interface ToolListEntry {
  name: string;
  description: string;
}

/** Full MCP interaction detail. */
export interface MCPInteractionDetailResponse {
  id: string;
  interaction_type: string;
  server_name: string;
  tool_name?: string;
  tool_arguments?: Record<string, unknown>;
  tool_result?: Record<string, unknown>;
  available_tools?: ToolListEntry[];
  duration_ms?: number;
  error_message?: string;
  created_at: string;
}
