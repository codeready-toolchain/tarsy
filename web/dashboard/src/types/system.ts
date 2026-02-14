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

/** MCP server status. */
export interface MCPServerStatus {
  id: string;
  healthy: boolean;
  last_check: string;
  tool_count: number;
  tools: string[];
  error: string | null;
}

/** MCP servers response. */
export interface MCPServersResponse {
  servers: MCPServerStatus[];
}

/** Default tools response. */
export interface DefaultToolsResponse {
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

/** Health response. */
export interface HealthResponse {
  status: string;
  version: string;
  database: {
    status: string;
    latency_ms: number;
  };
  phase: string;
  configuration: {
    agents: number;
    chains: number;
    mcp_servers: number;
    llm_providers: number;
  };
  worker_pool?: {
    max_workers: number;
    active_workers: number;
    pending_sessions: number;
  };
  mcp_health?: Record<string, {
    healthy: boolean;
    last_check: string;
    error?: string;
  }>;
  warnings?: SystemWarning[];
}
