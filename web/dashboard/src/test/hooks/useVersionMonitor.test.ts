/**
 * Tests for useVersionMonitor hook.
 *
 * Covers: backend version fetch, dashboard version check,
 *         consecutive mismatch detection, and refresh.
 */

import { renderHook, waitFor } from '@testing-library/react';

vi.mock('../../services/api.ts', () => ({
  getHealth: vi.fn(),
  handleAPIError: vi.fn((e: unknown) => String(e)),
}));

vi.mock('../../config/env.ts', () => ({
  config: { isDevelopment: false, isProduction: true },
  DASHBOARD_VERSION: 'v1.0.0',
  urls: {
    api: { base: '', health: '/health' },
    websocket: { base: 'ws://localhost:8080', path: '/api/v1/ws' },
    oauth: { signIn: '/oauth2/sign_in', signOut: '/oauth2/sign_out', userInfo: '/oauth2/userinfo' },
  },
}));

import { useVersionMonitor } from '../../hooks/useVersionMonitor';
import { getHealth } from '../../services/api';

const mockGetHealth = vi.mocked(getHealth);
let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  fetchMock = vi.fn();
  global.fetch = fetchMock;

  mockGetHealth.mockResolvedValue({
    status: 'healthy',
    version: 'v1.0.0',
  } as any);

  fetchMock.mockResolvedValue({
    ok: true,
    text: () => Promise.resolve('<html><meta name="app-version" content="v1.0.0"></html>'),
  });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('useVersionMonitor', () => {
  it('fetches backend version on mount', async () => {
    const { result } = renderHook(() => useVersionMonitor());

    await waitFor(() => {
      expect(result.current.backendVersion).toBe('v1.0.0');
    });
    expect(result.current.backendStatus).toBe('healthy');
    expect(mockGetHealth).toHaveBeenCalled();
  });

  it('sets error status on health check failure', async () => {
    mockGetHealth.mockRejectedValue(new Error('network error'));

    const { result } = renderHook(() => useVersionMonitor());

    await waitFor(() => {
      expect(result.current.backendStatus).toBe('error');
    });
  });

  it('starts with checking status', () => {
    const { result } = renderHook(() => useVersionMonitor());
    expect(result.current.backendStatus).toBe('checking');
    expect(result.current.backendVersion).toBeNull();
    expect(result.current.dashboardVersionChanged).toBe(false);
  });

  it('does not show banner on first poll even with version mismatch', async () => {
    fetchMock.mockResolvedValue({
      ok: true,
      text: () => Promise.resolve('<html><meta name="app-version" content="v2.0.0"></html>'),
    });

    const { result } = renderHook(() => useVersionMonitor());

    await waitFor(() => {
      expect(result.current.backendVersion).toBe('v1.0.0');
    });

    // Even with a different version in index.html, no banner on first poll
    expect(result.current.dashboardVersionChanged).toBe(false);
  });

  it('skips dashboard version check when placeholder is present', async () => {
    fetchMock.mockResolvedValue({
      ok: true,
      text: () =>
        Promise.resolve('<html><meta name="app-version" content="%VITE_APP_VERSION%"></html>'),
    });

    const { result } = renderHook(() => useVersionMonitor());

    await waitFor(() => {
      expect(result.current.backendVersion).toBe('v1.0.0');
    });

    // No dashboard version change detected for dev placeholder
    expect(result.current.dashboardVersionChanged).toBe(false);
  });

  it('refresh manually triggers both checks', async () => {
    const { result } = renderHook(() => useVersionMonitor());

    await waitFor(() => {
      expect(result.current.backendVersion).toBe('v1.0.0');
    });

    mockGetHealth.mockResolvedValue({ status: 'healthy', version: 'v2.0.0' } as any);

    await result.current.refresh();

    await waitFor(() => {
      expect(result.current.backendVersion).toBe('v2.0.0');
    });
  });

  it('sets unknown version when health response has no version', async () => {
    mockGetHealth.mockResolvedValue({ status: 'healthy' } as any);

    const { result } = renderHook(() => useVersionMonitor());

    await waitFor(() => {
      expect(result.current.backendVersion).toBe('unknown');
    });
  });
});
