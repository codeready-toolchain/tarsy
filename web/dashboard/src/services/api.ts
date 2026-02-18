/**
 * REST API client for TARSy backend.
 *
 * Axios-based with:
 * - Base URL from environment config
 * - Retry on temporary errors (502/503/504, network errors)
 * - 401 → auth redirect
 * - All endpoint methods typed
 */

import axios, { type AxiosInstance, type AxiosError } from 'axios';
import { urls } from '../config/env.ts';
import { authService } from './auth.ts';
import type {
  DashboardListResponse,
  DashboardListParams,
  SubmitAlertRequest,
  AlertResponse,
  CancelResponse,
  SendChatMessageResponse,
} from '../types/api.ts';
import type {
  SessionDetailResponse,
  SessionSummaryResponse,
  ActiveSessionsResponse,
  TimelineEvent,
} from '../types/session.ts';
import type {
  TraceListResponse,
  LLMInteractionDetailResponse,
  MCPInteractionDetailResponse,
} from '../types/trace.ts';
import type {
  HealthResponse,
  SystemWarningsResponse,
  MCPServersResponse,
  DefaultToolsResponse,
  AlertTypesResponse,
  FilterOptionsResponse,
} from '../types/system.ts';

// ────────────────────────────────────────────────────────────
// Axios instance
// ────────────────────────────────────────────────────────────

const client: AxiosInstance = axios.create({
  baseURL: urls.api.base,
  timeout: 10_000,
  withCredentials: true,
  headers: {
    'Content-Type': 'application/json',
  },
});

// Response interceptor: handle 401 → auth redirect
client.interceptors.response.use(
  (response) => response,
  (error: AxiosError) => {
    if (error.response?.status === 401) {
      authService.handleAuthError();
    }
    return Promise.reject(error);
  },
);

// ────────────────────────────────────────────────────────────
// Retry helper
// ────────────────────────────────────────────────────────────

/**
 * Retry a request on temporary errors (network errors, 502/503/504).
 * Exponential backoff: 500ms, 1s, 2s, 4s, capped at 5s.
 */
async function retryOnTemporaryError<T>(
  fn: () => Promise<T>,
  maxRetries: number = 3,
): Promise<T> {
  let lastError: unknown;
  for (let attempt = 0; attempt <= maxRetries; attempt++) {
    try {
      return await fn();
    } catch (error) {
      lastError = error;
      if (attempt === maxRetries || !isTemporaryError(error)) {
        throw error;
      }
      const delay = Math.min(500 * Math.pow(2, attempt), 5000);
      await new Promise((resolve) => setTimeout(resolve, delay));
    }
  }
  throw lastError;
}

function isTemporaryError(error: unknown): boolean {
  if (!axios.isAxiosError(error)) return false;
  // Cancelled requests are not temporary — don't retry them
  if (axios.isCancel(error)) return false;
  // Network errors (no response)
  if (!error.response) return true;
  // Gateway errors
  const status = error.response.status;
  return status === 502 || status === 503 || status === 504;
}

// ────────────────────────────────────────────────────────────
// API methods
// ────────────────────────────────────────────────────────────

/** User-facing error message from an API error. */
export function handleAPIError(error: unknown): string {
  if (axios.isAxiosError(error)) {
    if (error.response?.data?.message) {
      return error.response.data.message;
    }
    if (error.response?.status) {
      return `Request failed with status ${error.response.status}`;
    }
    return 'Network error — please check your connection';
  }
  return 'An unexpected error occurred';
}

// --- Sessions ---

export async function getSessions(params: DashboardListParams): Promise<DashboardListResponse> {
  const response = await retryOnTemporaryError(() =>
    client.get<DashboardListResponse>('/api/v1/sessions', { params }),
  );
  return response.data;
}

export async function getActiveSessions(): Promise<ActiveSessionsResponse> {
  const response = await retryOnTemporaryError(() =>
    client.get<ActiveSessionsResponse>('/api/v1/sessions/active'),
  );
  return response.data;
}

export async function getSession(id: string): Promise<SessionDetailResponse> {
  const response = await client.get<SessionDetailResponse>(`/api/v1/sessions/${id}`);
  return response.data;
}

export async function getSessionSummary(id: string): Promise<SessionSummaryResponse> {
  const response = await client.get<SessionSummaryResponse>(`/api/v1/sessions/${id}/summary`);
  return response.data;
}

export async function getTimeline(id: string): Promise<TimelineEvent[]> {
  const response = await client.get<TimelineEvent[]>(`/api/v1/sessions/${id}/timeline`);
  return response.data;
}

export async function cancelSession(id: string): Promise<CancelResponse> {
  const response = await client.post<CancelResponse>(`/api/v1/sessions/${id}/cancel`);
  return response.data;
}

export async function sendChatMessage(
  sessionId: string,
  content: string,
): Promise<SendChatMessageResponse> {
  const response = await client.post<SendChatMessageResponse>(
    `/api/v1/sessions/${sessionId}/chat/messages`,
    { content },
  );
  return response.data;
}

// --- Trace ---

export async function getTrace(id: string): Promise<TraceListResponse> {
  const response = await client.get<TraceListResponse>(`/api/v1/sessions/${id}/trace`);
  return response.data;
}

export async function getLLMInteraction(
  sessionId: string,
  interactionId: string,
): Promise<LLMInteractionDetailResponse> {
  const response = await client.get<LLMInteractionDetailResponse>(
    `/api/v1/sessions/${sessionId}/trace/llm/${interactionId}`,
  );
  return response.data;
}

export async function getMCPInteraction(
  sessionId: string,
  interactionId: string,
): Promise<MCPInteractionDetailResponse> {
  const response = await client.get<MCPInteractionDetailResponse>(
    `/api/v1/sessions/${sessionId}/trace/mcp/${interactionId}`,
  );
  return response.data;
}

// --- Filters ---

export async function getFilterOptions(): Promise<FilterOptionsResponse> {
  const response = await retryOnTemporaryError(() =>
    client.get<FilterOptionsResponse>('/api/v1/sessions/filter-options'),
  );
  return response.data;
}

// --- System ---

export async function getHealth(): Promise<HealthResponse> {
  const response = await client.get<HealthResponse>('/health');
  return response.data;
}

export async function getSystemWarnings(): Promise<SystemWarningsResponse> {
  const response = await client.get<SystemWarningsResponse>('/api/v1/system/warnings');
  return response.data;
}

export async function getMCPServers(): Promise<MCPServersResponse> {
  const response = await client.get<MCPServersResponse>('/api/v1/system/mcp-servers');
  return response.data;
}

export async function getDefaultTools(alertType?: string): Promise<DefaultToolsResponse> {
  const params = alertType ? { alert_type: alertType } : undefined;
  const response = await client.get<DefaultToolsResponse>('/api/v1/system/default-tools', {
    params,
  });
  return response.data;
}

export async function getAlertTypes(): Promise<AlertTypesResponse> {
  const response = await client.get<AlertTypesResponse>('/api/v1/alert-types');
  return response.data;
}

// --- Runbooks ---

export async function getRunbooks(): Promise<string[]> {
  try {
    const response = await client.get<string[]>('/api/v1/runbooks');
    return response.data;
  } catch {
    return [];
  }
}

// --- Alerts ---

export async function submitAlert(data: SubmitAlertRequest): Promise<AlertResponse> {
  const response = await client.post<AlertResponse>('/api/v1/alerts', data);
  return response.data;
}
