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
