/**
 * Tests for api.ts
 *
 * Covers: handleAPIError and key API endpoint wiring.
 * The retry logic (retryOnTemporaryError, isTemporaryError) is internal —
 * tested indirectly via the retry behavior of getSessions / getActiveSessions.
 */

import axios from 'axios';
import type { AxiosError } from 'axios';

// We need to mock modules before importing the SUT
vi.mock('axios', () => {
  const mockClient = {
    get: vi.fn(),
    post: vi.fn(),
    interceptors: {
      response: { use: vi.fn() },
    },
  };
  return {
    default: {
      create: vi.fn(() => mockClient),
      isAxiosError: vi.fn((error: unknown) => (error as Record<string, unknown>)?.isAxiosError === true),
      isCancel: vi.fn((error: unknown) => (error as Record<string, unknown>)?.__CANCEL__ === true),
    },
  };
});

vi.mock('../../services/auth.ts', () => ({
  authService: { handleAuthError: vi.fn() },
}));

vi.mock('../../config/env.ts', () => ({
  config: { isDevelopment: false, isProduction: true },
  DASHBOARD_VERSION: 'test',
  urls: {
    api: { base: '', health: '/health' },
    websocket: { base: 'ws://localhost:8080', path: '/api/v1/ws' },
    oauth: { signIn: '/oauth2/sign_in', signOut: '/oauth2/sign_out', userInfo: '/oauth2/userinfo' },
  },
}));

import {
  handleAPIError,
  getSessions,
  getActiveSessions,
  getSession,
  cancelSession,
  submitAlert,
  getHealth,
  getDefaultTools,
} from '../../services/api';

function getMockClient() {
  return (axios.create as ReturnType<typeof vi.fn>).mock.results[0].value;
}

function makeAxiosError(status: number | null, message?: string): AxiosError {
  const error: Record<string, unknown> = {
    isAxiosError: true,
    message: message || 'Request failed',
    response: status ? { status, data: message ? { message } : {} } : undefined,
  };
  return error as unknown as AxiosError;
}

// ---------------------------------------------------------------------------
// handleAPIError
// ---------------------------------------------------------------------------

describe('handleAPIError', () => {
  beforeEach(() => {
    (axios.isAxiosError as unknown as ReturnType<typeof vi.fn>).mockImplementation(
      (e: unknown) => (e as Record<string, unknown>)?.isAxiosError === true,
    );
  });

  it('extracts message from response data', () => {
    const error = makeAxiosError(400, 'Invalid input');
    expect(handleAPIError(error)).toBe('Invalid input');
  });

  it('returns status-based message when no data message', () => {
    const error: Record<string, unknown> = {
      isAxiosError: true,
      response: { status: 404, data: {} },
    };
    expect(handleAPIError(error)).toBe('Request failed with status 404');
  });

  it('returns network error for errors without response', () => {
    const error = makeAxiosError(null);
    expect(handleAPIError(error)).toBe('Network error — please check your connection');
  });

  it('returns generic message for non-Axios errors', () => {
    (axios.isAxiosError as unknown as ReturnType<typeof vi.fn>).mockReturnValue(false);
    expect(handleAPIError(new Error('boom'))).toBe('An unexpected error occurred');
  });
});

// ---------------------------------------------------------------------------
// API endpoint wiring
// ---------------------------------------------------------------------------

describe('API methods', () => {
  let client: ReturnType<typeof getMockClient>;

  beforeEach(() => {
    client = getMockClient();
    client.get.mockReset();
    client.post.mockReset();
  });

  describe('getSessions', () => {
    it('calls correct endpoint with params', async () => {
      client.get.mockResolvedValue({ data: { sessions: [], total: 0 } });
      const params = { page: 1, page_size: 25, status: 'completed' };
      const result = await getSessions(params as any);
      expect(client.get).toHaveBeenCalledWith('/api/v1/sessions', { params });
      expect(result).toEqual({ sessions: [], total: 0 });
    });

    it('throws on non-retryable error', async () => {
      const error = makeAxiosError(404, 'Not Found');
      client.get.mockRejectedValue(error);

      await expect(getSessions({} as any)).rejects.toThrow();
      // Non-retryable errors should only call once
      expect(client.get).toHaveBeenCalledTimes(1);
    });
  });

  describe('getActiveSessions', () => {
    it('calls correct endpoint', async () => {
      client.get.mockResolvedValue({ data: { active: [], queued: [] } });
      const result = await getActiveSessions();
      expect(client.get).toHaveBeenCalledWith('/api/v1/sessions/active');
      expect(result).toEqual({ active: [], queued: [] });
    });
  });

  describe('getSession', () => {
    it('calls correct endpoint with id', async () => {
      client.get.mockResolvedValue({ data: { id: 's1' } });
      await getSession('s1');
      expect(client.get).toHaveBeenCalledWith('/api/v1/sessions/s1');
    });
  });

  describe('cancelSession', () => {
    it('calls correct endpoint', async () => {
      client.post.mockResolvedValue({ data: { session_id: 's1', message: 'cancelling' } });
      const result = await cancelSession('s1');
      expect(client.post).toHaveBeenCalledWith('/api/v1/sessions/s1/cancel');
      expect(result.message).toBe('cancelling');
    });
  });

  describe('submitAlert', () => {
    it('calls correct endpoint with data', async () => {
      client.post.mockResolvedValue({ data: { session_id: 's1' } });
      const data = { alert_data: '{}', alert_type: 'test' };
      await submitAlert(data as any);
      expect(client.post).toHaveBeenCalledWith('/api/v1/alerts', data);
    });
  });

  describe('getHealth', () => {
    it('calls health endpoint', async () => {
      client.get.mockResolvedValue({ data: { status: 'healthy', version: 'v1' } });
      const result = await getHealth();
      expect(client.get).toHaveBeenCalledWith('/health');
      expect(result.status).toBe('healthy');
    });
  });

  describe('getDefaultTools', () => {
    it('passes alert_type as query param when provided', async () => {
      client.get.mockResolvedValue({ data: { tools: [] } });
      await getDefaultTools('prometheus');
      expect(client.get).toHaveBeenCalledWith('/api/v1/system/default-tools', {
        params: { alert_type: 'prometheus' },
      });
    });

    it('omits params when no alert type', async () => {
      client.get.mockResolvedValue({ data: { tools: [] } });
      await getDefaultTools();
      expect(client.get).toHaveBeenCalledWith('/api/v1/system/default-tools', {
        params: undefined,
      });
    });
  });
});
