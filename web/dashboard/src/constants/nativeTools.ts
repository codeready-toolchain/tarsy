/**
 * Constants for Google/Gemini native tools configuration.
 *
 * These constants match the Go backend's native tool enum values
 * (pkg/config/types.go NativeToolType) and should be kept in sync.
 */

export const NATIVE_TOOL_NAMES = {
  GOOGLE_SEARCH: 'google_search',
  CODE_EXECUTION: 'code_execution',
  URL_CONTEXT: 'url_context',
} as const;

/** Type for native tool names. */
export type NativeToolName = (typeof NATIVE_TOOL_NAMES)[keyof typeof NATIVE_TOOL_NAMES];

/** Display labels for native tools. */
export const NATIVE_TOOL_LABELS: Record<NativeToolName, string> = {
  [NATIVE_TOOL_NAMES.GOOGLE_SEARCH]: 'Google Search',
  [NATIVE_TOOL_NAMES.CODE_EXECUTION]: 'Code Execution',
  [NATIVE_TOOL_NAMES.URL_CONTEXT]: 'URL Context',
} as const;

/** Descriptions for native tools. */
export const NATIVE_TOOL_DESCRIPTIONS: Record<NativeToolName, string> = {
  [NATIVE_TOOL_NAMES.GOOGLE_SEARCH]: 'Enable web search capability',
  [NATIVE_TOOL_NAMES.CODE_EXECUTION]: 'Enable Python code execution in sandbox',
  [NATIVE_TOOL_NAMES.URL_CONTEXT]: 'Enable URL grounding for specific web pages',
} as const;
