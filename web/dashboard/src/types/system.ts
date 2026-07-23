/**
 * System-related types derived from Go API handlers (pkg/api/handler_system.go, etc.).
 */

/** System warning item. */
export interface SystemWarning {
  id: string;
  category: string;
  message: string;
  details: string;
  server_id?: string;
  created_at: string;
}

/** System warnings response. */
export interface SystemWarningsResponse {
  warnings: SystemWarning[];
}

// ── MCP tool & selection types ──────────────────────────────────

/** MCP tool info returned by the mcp-servers endpoint. */
export interface MCPToolInfo {
  name: string;
  description: string;
}

/** A selected MCP server with optional tool filtering (for alert override). */
export interface MCPServerSelection {
  name: string;
  tools?: string[] | null; // null/undefined = all tools, array = specific tools
}

/** Native LLM provider tools configuration. */
export interface NativeToolsConfig {
  google_search?: boolean;
  code_execution?: boolean;
  url_context?: boolean;
}

/** Per-alert MCP selection override sent to POST /api/v1/alerts. */
export interface MCPSelectionConfig {
  servers: MCPServerSelection[];
  native_tools?: NativeToolsConfig;
}

// ── MCP server status ───────────────────────────────────────────

/** MCP server status. */
export interface MCPServerStatus {
  id: string;
  healthy: boolean;
  last_check: string;
  tool_count: number;
  tools: MCPToolInfo[];
  error: string | null;
}

/** MCP servers response. */
export interface MCPServersResponse {
  servers: MCPServerStatus[];
}

/** Default tools response (GET /api/v1/system/default-tools). */
export interface DefaultToolsResponse {
  alert_type?: string;
  mcp_servers: string[];
  native_tools: Record<string, boolean>;
}

/** Alert type info. */
export interface AlertTypeInfo {
  type: string;
  chain_id: string;
  description: string;
}

/** Alert types response. */
export interface AlertTypesResponse {
  alert_types: AlertTypeInfo[];
  default_chain_id: string;
  default_alert_type: string;
}

/** Filter options response. */
export interface FilterOptionsResponse {
  alert_types: string[];
  chain_ids: string[];
  statuses: string[];
}

// ── Health endpoint types (matches pkg/api/responses.go, pkg/database/health.go, pkg/queue/types.go) ──

/** Database health and connection pool statistics. */
export interface DatabaseHealthStatus {
  status: string;
  response_time_ms: number;
  open_connections: number;
  in_use: number;
  idle: number;
  wait_count: number;
  wait_duration_ms: number;
  max_open_conns: number;
}

/** Single worker health info. */
export interface WorkerHealth {
  id: string;
  status: 'idle' | 'working';
  current_session_id?: string;
  sessions_processed: number;
  last_activity: string;
}

/** Worker pool health. */
export interface PoolHealth {
  is_healthy: boolean;
  db_reachable: boolean;
  db_error?: string;
  pod_id: string;
  active_workers: number;
  total_workers: number;
  active_sessions: number;
  max_concurrent: number;
  queue_depth: number;
  worker_stats: WorkerHealth[];
  last_orphan_scan: string;
  orphans_recovered: number;
}

/** MCP server health status from HealthMonitor. */
export interface MCPHealthStatus {
  server_id: string;
  healthy: boolean;
  last_check: string;
  error?: string;
  tool_count: number;
}

/** Health response (GET /health). */
export interface HealthResponse {
  status: string;
  version: string;
  database: DatabaseHealthStatus;
  phase: string;
  configuration: {
    agents: number;
    chains: number;
    mcp_servers: number;
    llm_providers: number;
  };
  worker_pool?: PoolHealth;
  mcp_health?: Record<string, MCPHealthStatus>;
  warnings?: SystemWarning[];
}

// ── System config viewer (GET /api/v1/system/config) ─────────

/** Sanitized MCP transport (secrets redacted). */
export interface SanitizedTransport {
  type: string;
  command?: string;
  args?: string[];
  url?: string;
  verify_ssl?: boolean | null;
  timeout?: number;
  env_keys?: string[];
  bearer_token_set: boolean;
}

/** MCP server config view. */
export interface MCPServerConfigView {
  transport: SanitizedTransport;
  instructions?: string;
  data_masking?: Record<string, unknown> | null;
  summarization?: Record<string, unknown> | null;
}

/** Agent config view. */
export interface AgentConfigView {
  type?: string;
  description?: string;
  mcp_servers?: string[];
  custom_instructions: string;
  llm_backend?: string;
  max_iterations?: number | null;
  native_tools?: Record<string, boolean>;
  orchestrator?: OrchestratorView | null;
  skills?: string[] | null;
  required_skills?: string[];
}

export interface OrchestratorView {
  max_concurrent_agents?: number | null;
  agent_timeout?: string | null;
  max_budget?: string | null;
}

export interface FallbackProviderView {
  provider: string;
  backend: string;
}

export interface SubAgentView {
  name: string;
  llm_provider?: string;
  llm_backend?: string;
  max_iterations?: number | null;
  mcp_servers?: string[];
  required_skills?: string[];
  skills?: string[];
}

export interface StageAgentView {
  name: string;
  type?: string;
  llm_provider?: string;
  llm_backend?: string;
  max_iterations?: number | null;
  mcp_servers?: string[];
  sub_agents?: SubAgentView[];
  fallback_providers?: FallbackProviderView[];
  required_skills?: string[];
  skills?: string[];
}

export interface StageView {
  name: string;
  agents: StageAgentView[];
  replicas?: number;
  success_policy?: string;
  max_iterations?: number | null;
  mcp_servers?: string[];
  fallback_providers?: FallbackProviderView[];
  sub_agents?: SubAgentView[];
  synthesis?: {
    agent?: string;
    llm_backend?: string;
    llm_provider?: string;
  } | null;
}

export interface ChatView {
  enabled: boolean;
  agent?: string;
  llm_backend?: string;
  llm_provider?: string;
  mcp_servers?: string[];
  max_iterations?: number | null;
  sub_agents?: SubAgentView[];
}

export interface ScoringView {
  enabled: boolean;
  agent?: string;
  llm_backend?: string;
  llm_provider?: string;
  mcp_servers?: string[];
  max_iterations?: number | null;
}

export interface ChainConfigView {
  alert_types: string[];
  description?: string;
  stages: StageView[];
  chat?: ChatView | null;
  scoring?: ScoringView | null;
  llm_provider?: string;
  executive_summary_provider?: string;
  llm_backend?: string;
  fallback_providers?: FallbackProviderView[];
  max_iterations?: number | null;
  mcp_servers?: string[];
  sub_agents?: SubAgentView[];
}

export interface LLMProviderConfigView {
  type: string;
  model: string;
  api_key_env?: string;
  credentials_env?: string;
  project_env?: string;
  location_env?: string;
  base_url?: string;
  max_tool_result_tokens: number;
  native_tools?: Record<string, boolean>;
}

export interface SkillMetaView {
  name: string;
  description: string;
}

export interface DefaultsView {
  llm_provider?: string;
  max_iterations?: number | null;
  llm_backend?: string;
  fallback_providers?: FallbackProviderView[];
  scoring?: ScoringView | null;
  success_policy?: string;
  alert_type?: string;
  runbook?: string;
  alert_masking?: { enabled: boolean; pattern_group: string } | null;
  orchestrator?: OrchestratorView | null;
  memory?: {
    enabled: boolean;
    max_inject?: number;
    reflector_memory_limit?: number;
    embedding?: {
      provider?: string;
      model?: string;
      api_key_env?: string;
      dimensions?: number;
      base_url?: string;
    };
  } | null;
}

export interface QueueView {
  worker_count: number;
  max_concurrent_sessions: number;
  poll_interval: string;
  poll_interval_jitter: string;
  session_timeout: string;
  graceful_shutdown_timeout: string;
  scoring_shutdown_timeout: string;
  orphan_detection_interval: string;
  orphan_threshold: string;
  heartbeat_interval: string;
}

export interface SystemSettingsView {
  github?: { token_env?: string } | null;
  slack?: { enabled: boolean; token_env?: string; channel?: string } | null;
  runbooks?: {
    repo_url?: string;
    cache_ttl?: string;
    allowed_domains?: string[];
  } | null;
  retention?: {
    session_retention_days: number;
    event_ttl: string;
    cleanup_interval: string;
  } | null;
  cost_estimation?: {
    enabled: boolean;
    model_rates?: Record<string, { input_per_million: number; output_per_million: number }>;
    catalog: {
      source: string;
      entry_count: number;
      last_fetch?: string | null;
      last_error?: string;
    };
  } | null;
  dashboard_url?: string;
  allowed_ws_origins: string[];
}

/** Full sanitized config snapshot. */
export interface SystemConfigResponse {
  defaults: DefaultsView | null;
  queue: QueueView | null;
  system: SystemSettingsView;
  agents: Record<string, AgentConfigView>;
  chains: Record<string, ChainConfigView>;
  mcp_servers: Record<string, MCPServerConfigView>;
  llm_providers: Record<string, LLMProviderConfigView>;
  skills: Record<string, SkillMetaView>;
}

/** Skill detail with body (GET /api/v1/system/config/skills/:name). */
export interface SystemConfigSkillResponse {
  name: string;
  description: string;
  body: string;
}
