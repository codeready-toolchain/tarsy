/**
 * LLM and MCP interaction type constants.
 *
 * Values match the Go backend (pkg/models/interaction.go).
 */

/** LLM interaction types (from LLMInteraction.InteractionType). */
export const LLM_INTERACTION_TYPE = {
  ITERATION: 'iteration',
  SUMMARIZATION: 'summarization',
  FINAL_ANALYSIS: 'final_analysis',
  EXECUTIVE_SUMMARY: 'executive_summary',
  CHAT_RESPONSE: 'chat_response',
} as const;

export type LLMInteractionType =
  (typeof LLM_INTERACTION_TYPE)[keyof typeof LLM_INTERACTION_TYPE];

/** MCP interaction types (from MCPInteraction.InteractionType). */
export const MCP_INTERACTION_TYPE = {
  TOOL_CALL: 'tool_call',
  TOOL_LIST: 'tool_list',
} as const;

export type MCPInteractionType =
  (typeof MCP_INTERACTION_TYPE)[keyof typeof MCP_INTERACTION_TYPE];

/** Well-known MCP tool name that indicates a tool-list operation. */
export const MCP_LIST_TOOLS_NAME = 'list_tools';
