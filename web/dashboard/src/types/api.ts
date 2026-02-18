/**
 * API request/response wrapper types.
 */

import type { DashboardSessionItem } from './session.ts';
import type { MCPSelectionConfig } from './system.ts';

/** Pagination info in list responses. */
export interface PaginationInfo {
  page: number;
  page_size: number;
  total_pages: number;
  total_items: number;
}

/** Paginated session list response. */
export interface DashboardListResponse {
  sessions: DashboardSessionItem[];
  pagination: PaginationInfo;
}

/** Query parameters for the dashboard session list. */
export interface DashboardListParams {
  page?: number;
  page_size?: number;
  sort_by?: string;
  sort_order?: 'asc' | 'desc';
  status?: string;
  alert_type?: string;
  chain_id?: string;
  search?: string;
  start_date?: string;
  end_date?: string;
}

/**
 * Alert submission request.
 * Field names match Go backend JSON tags (pkg/api/requests.go).
 * - `data`: alert payload text (Go: json:"data")
 * - `alert_type`: optional, Go resolves chain from this (Go: json:"alert_type")
 * - `runbook`: optional runbook URL (Go: json:"runbook")
 * - `mcp`: optional MCP selection override (Go: json:"mcp")
 * Note: `author` is extracted from X-Forwarded-User header, not request body.
 */
export interface SubmitAlertRequest {
  data: string;
  alert_type?: string;
  runbook?: string;
  mcp?: MCPSelectionConfig;
  slack_message_fingerprint?: string;
}

/** Alert submission response. */
export interface AlertResponse {
  session_id: string;
  status: string;
  message: string;
}

/** Cancel session response. */
export interface CancelResponse {
  session_id: string;
  message: string;
}

/** Chat message request. */
export interface SendChatMessageRequest {
  content: string;
}

/** Chat message response (202 Accepted). */
export interface SendChatMessageResponse {
  chat_id: string;
  message_id: string;
  stage_id: string;
}
