/**
 * API request/response wrapper types.
 */

import type { CostCompleteness, DashboardSessionItem } from './session.ts';
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
  cost_estimation_enabled?: boolean;
}

/** Ranking for Usage summary top sessions. */
export type UsageRankBy = 'cost' | 'tokens';

/** Query parameters for GET /api/v1/usage/summary. */
export interface UsageSummaryParams {
  start_date: string;
  end_date: string;
  alert_type?: string;
  chain_id?: string;
  rank_by?: UsageRankBy;
}

/** Window echoed by the usage summary endpoint. */
export interface UsageWindow {
  start: string;
  end: string;
}

/** Fleet-wide token/cost rollup for a usage window. */
export interface UsageTotals {
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  estimated_cost_usd?: number | null;
  cost_completeness?: CostCompleteness;
  unpriced_interaction_count?: number;
}

/** Per-model rollup within a usage window. */
export interface UsageModelBreakdown {
  model_name: string;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  estimated_cost_usd?: number | null;
  priced?: boolean;
}

/** Per-alert-type rollup within a usage window. */
export interface UsageAlertBreakdown {
  alert_type: string;
  total_tokens: number;
  estimated_cost_usd?: number | null;
}

/** Per-chain rollup within a usage window. */
export interface UsageChainBreakdown {
  chain_id: string;
  total_tokens: number;
  estimated_cost_usd?: number | null;
}

/** One of the capped top sessions in a usage window. */
export interface UsageTopSession {
  session_id: string;
  alert_type: string | null;
  chain_id: string;
  total_tokens: number;
  estimated_cost_usd?: number | null;
  cost_completeness?: CostCompleteness;
  created_at: string;
}

/** Response from GET /api/v1/usage/summary. */
export interface UsageSummaryResponse {
  cost_estimation_enabled: boolean;
  window: UsageWindow;
  rank_by: UsageRankBy;
  totals: UsageTotals;
  by_model: UsageModelBreakdown[];
  by_alert_type: UsageAlertBreakdown[];
  by_chain: UsageChainBreakdown[];
  top_sessions: UsageTopSession[];
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
  tool_improvement_report: string | null;
  failure_tags: string[] | null;
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

export type TriageGroupKey = 'investigating' | 'needs_review' | 'in_progress' | 'reviewed';

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

/** Allowed review workflow actions. */
export const REVIEW_ACTION = {
  CLAIM: 'claim',
  UNCLAIM: 'unclaim',
  COMPLETE: 'complete',
  REOPEN: 'reopen',
  UPDATE_FEEDBACK: 'update_feedback',
  ACKNOWLEDGE: 'acknowledge',
} as const;

export type ReviewAction = (typeof REVIEW_ACTION)[keyof typeof REVIEW_ACTION];

/** Possible review_status values on a session. */
export const REVIEW_STATUS = {
  NEEDS_REVIEW: 'needs_review',
  IN_PROGRESS: 'in_progress',
  REVIEWED: 'reviewed',
} as const;

export type ReviewStatus = (typeof REVIEW_STATUS)[keyof typeof REVIEW_STATUS];

/** Review modal modes used by the dashboard UI. */
export const REVIEW_MODAL_MODE = {
  COMPLETE: 'complete',
  EDIT: 'edit',
} as const;

export type ReviewModalMode = (typeof REVIEW_MODAL_MODE)[keyof typeof REVIEW_MODAL_MODE];

/** Returns the modal mode for a given review_status and quality_rating. */
export function getReviewModalMode(
  reviewStatus: string | null | undefined,
  qualityRating: string | null | undefined,
): ReviewModalMode {
  if (reviewStatus === REVIEW_STATUS.REVIEWED) {
    return qualityRating ? REVIEW_MODAL_MODE.EDIT : REVIEW_MODAL_MODE.COMPLETE;
  }
  return REVIEW_MODAL_MODE.COMPLETE;
}

/** Allowed quality rating values for review feedback. */
export const QUALITY_RATING = {
  ACCURATE: 'accurate',
  PARTIALLY_ACCURATE: 'partially_accurate',
  INACCURATE: 'inaccurate',
} as const;

export type QualityRating = (typeof QUALITY_RATING)[keyof typeof QUALITY_RATING];

/**
 * UI-only selection values for the review radio group.
 * Combines quality ratings with an "acknowledge" sentinel that triggers
 * a different backend action rather than a quality judgment.
 */
export const REVIEW_SELECTION = {
  ...QUALITY_RATING,
  ACKNOWLEDGE: 'acknowledge',
} as const;

export type ReviewSelection = (typeof REVIEW_SELECTION)[keyof typeof REVIEW_SELECTION];

/** Request body for PATCH /api/v1/sessions/review. */
export interface UpdateReviewRequest {
  session_ids: string[];
  action: ReviewAction;
  quality_rating?: string;
  action_taken?: string;
  investigation_feedback?: string;
}

/** Per-session result from a review action. */
export interface UpdateReviewResult {
  session_id: string;
  success: boolean;
  error?: string;
}

/** Response from PATCH /api/v1/sessions/review. */
export interface UpdateReviewResponse {
  results: UpdateReviewResult[];
}

/** Single entry in the review activity log. */
export interface ReviewActivityItem {
  id: string;
  actor: string;
  action: string;
  from_status: string | null;
  to_status: string;
  quality_rating?: string | null;
  note?: string | null;
  investigation_feedback?: string | null;
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
