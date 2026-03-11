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
  scoring_status?: string;
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

/** Full score details from GET /sessions/:id/score. */
export interface SessionScoreResponse {
  score_id: string;
  total_score: number | null;
  score_analysis: string | null;
  missing_tools_analysis: string | null;
  prompt_hash: string | null;
  score_triggered_by: string;
  status: string;
  stage_id: string | null;
  started_at: string;
  completed_at: string | null;
  error_message: string | null;
}

/** Response from POST /sessions/:id/score (202 Accepted). */
export interface ScoreSessionResponse {
  score_id: string;
}

// --- Triage / Review ---

export type TriageGroupKey = 'investigating' | 'needs_review' | 'in_progress' | 'resolved';

/** Paginated response for a single triage group. */
export interface TriageGroup {
  count: number;
  page: number;
  page_size: number;
  total_pages: number;
  sessions: DashboardSessionItem[];
}

/** Query parameters for GET /sessions/triage/:group. */
export interface TriageGroupParams {
  page?: number;
  page_size?: number;
  assignee?: string;
}

/** Request body for PATCH /sessions/:id/review. */
export interface UpdateReviewRequest {
  action: string;
  resolution_reason?: string;
  note?: string;
}

/** Response from PATCH /sessions/:id/review. */
export interface UpdateReviewResponse {
  id: string;
  review_status: string;
  assignee: string | null;
  assigned_at: string | null;
  resolved_at: string | null;
  resolution_reason: string | null;
  resolution_note: string | null;
}

/** Single entry in the review activity log. */
export interface ReviewActivityItem {
  id: string;
  actor: string;
  action: string;
  from_status: string | null;
  to_status: string;
  resolution_reason?: string | null;
  note?: string | null;
  created_at: string;
}

/** Response from GET /sessions/:id/review-activity. */
export interface ReviewActivityResponse {
  activities: ReviewActivityItem[];
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
