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
