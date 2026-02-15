/**
 * Session-related types derived from Go models (pkg/models/session.go).
 */

/** Single session in the dashboard list with pre-computed stats. */
export interface DashboardSessionItem {
  id: string;
  alert_type: string | null;
  chain_id: string;
  status: string;
  author: string | null;
  created_at: string;
  started_at: string | null;
  completed_at: string | null;
  duration_ms: number | null;
  error_message: string | null;
  executive_summary: string | null;
  llm_interaction_count: number;
  mcp_interaction_count: number;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  total_stages: number;
  completed_stages: number;
  has_parallel_stages: boolean;
  chat_message_count: number;
  current_stage_index: number | null;
  current_stage_id: string | null;
}

/** Active (in-progress / cancelling) session. */
export interface ActiveSessionItem {
  id: string;
  alert_type: string | null;
  chain_id: string;
  status: string;
  author: string | null;
  created_at: string;
  started_at: string | null;
  current_stage_index: number | null;
  current_stage_id: string | null;
}

/** Pending session waiting for a worker. */
export interface QueuedSessionItem {
  id: string;
  alert_type: string | null;
  chain_id: string;
  status: string;
  author: string | null;
  created_at: string;
  queue_position: number;
}

/** Enriched session detail response. */
export interface SessionDetailResponse {
  // Core fields
  id: string;
  alert_data: string;
  alert_type: string | null;
  status: string;
  chain_id: string;
  author: string | null;
  error_message: string | null;
  final_analysis: string | null;
  executive_summary: string | null;
  executive_summary_error: string | null;
  runbook_url: string | null;
  mcp_selection?: Record<string, unknown>;

  // Timestamps
  created_at: string;
  started_at: string | null;
  completed_at: string | null;

  // Computed fields
  duration_ms: number | null;
  chat_enabled: boolean;
  chat_id: string | null;
  chat_message_count: number;
  total_stages: number;
  completed_stages: number;
  failed_stages: number;
  has_parallel_stages: boolean;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  llm_interaction_count: number;
  mcp_interaction_count: number;
  current_stage_index: number | null;
  current_stage_id: string | null;

  // Stage list
  stages: StageOverview[];
}

/** Summary of a stage within the session detail. */
export interface StageOverview {
  id: string;
  stage_name: string;
  stage_index: number;
  status: string;
  parallel_type: string | null;
  expected_agent_count: number;
  started_at: string | null;
  completed_at: string | null;
  executions?: ExecutionOverview[];
}

/** Summary of an agent execution within a stage. */
export interface ExecutionOverview {
  execution_id: string;
  agent_name: string;
  agent_index: number;
  status: string;
  iteration_strategy: string;
  llm_provider: string | null;
  started_at: string | null;
  completed_at: string | null;
  duration_ms: number | null;
  error_message: string | null;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
}

/** Session summary response. */
export interface SessionSummaryResponse {
  session_id: string;
  total_interactions: number;
  llm_interactions: number;
  mcp_interactions: number;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  total_duration_ms: number | null;
  chain_statistics: ChainStatistics;
}

/** Stage counts for session summary. */
export interface ChainStatistics {
  total_stages: number;
  completed_stages: number;
  failed_stages: number;
  current_stage_index: number | null;
}

/** Active sessions response. */
export interface ActiveSessionsResponse {
  active: ActiveSessionItem[];
  queued: QueuedSessionItem[];
}

/** Timeline event from GET /sessions/:id/timeline. */
export interface TimelineEvent {
  id: string;
  session_id: string;
  stage_id: string | null;
  execution_id: string | null;
  sequence_number: number;
  event_type: string;
  status: string;
  content: string;
  metadata: Record<string, unknown> | null;
  created_at: string;
  updated_at: string;
}
