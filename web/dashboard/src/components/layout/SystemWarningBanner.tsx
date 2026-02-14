/**
 * Dismissible banner for system warnings (MCP server issues, etc.).
 *
 * Shell component — wired to real system warnings polling in Phase 7.7.
 */
export function SystemWarningBanner() {
  // Phase 7.7: polls /api/v1/system/warnings periodically and displays active warnings.
  // For now, render nothing.
  return null;
}
