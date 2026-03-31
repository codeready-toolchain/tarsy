/**
 * Tool call type constants.
 *
 * Values match the Go backend (pkg/agent/controller/tool_execution.go ToolType).
 */

export const TOOL_TYPE = {
  MCP: 'mcp',
  ORCHESTRATOR: 'orchestrator',
  SKILL: 'skill',
  MEMORY: 'memory',
} as const;

export type ToolType = (typeof TOOL_TYPE)[keyof typeof TOOL_TYPE];

/**
 * Memory tool name constants.
 *
 * Values match the Go backend (pkg/memory/tool_executor.go).
 */
export const MEMORY_TOOL_NAME = {
  RECALL_PAST_INVESTIGATIONS: 'recall_past_investigations',
  SEARCH_PAST_SESSIONS: 'search_past_sessions',
} as const;
