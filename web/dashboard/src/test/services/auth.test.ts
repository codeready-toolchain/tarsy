/**
 * Tests for auth.ts
 *
 * Covers: checkAuthStatus, getCurrentUser, handleAuthError, redirectToLogin, logout
 */

vi.mock('../../config/env.ts', () => ({
  config: { isDevelopment: false, isProduction: true },
  DASHBOARD_VERSION: 'test',
  urls: {
    api: { base: '', health: '/health' },
    websocket: { base: 'ws://localhost:8080', path: '/api/v1/ws' },
    oauth: { signIn: '/oauth2/sign_in', signOut: '/oauth2/sign_out', userInfo: '/oauth2/userinfo' },
  },
}));

import { authService } from '../../services/auth';

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

let fetchMock: ReturnType<typeof vi.fn>;
let locationHrefSetter: ReturnType<typeof vi.fn>;
let locationPathname: string;
let locationSearch: string;

beforeEach(() => {
  fetchMock = vi.fn();
  global.fetch = fetchMock;

  locationHrefSetter = vi.fn();
  locationPathname = '/dashboard';
  locationSearch = '';

  Object.defineProperty(window, 'location', {
    value: {
      get pathname() { return locationPathname; },
      get search() { return locationSearch; },
      get origin() { return 'http://localhost:5173'; },
      get protocol() { return 'http:'; },
      get host() { return 'localhost:5173'; },
      set href(val: string) { locationHrefSetter(val); },
      get href() { return `http://localhost:5173${locationPathname}${locationSearch}`; },
    },
    writable: true,
    configurable: true,
  });
});

afterEach(() => {
  vi.restoreAllMocks();
});

function makeResponse(
  status: number,
  body?: unknown,
  options?: { redirected?: boolean; headers?: Record<string, string> },
): Response {
  const headers = new Headers(
    options?.headers || { 'content-type': 'application/json' },
  );
  return {
    ok: status >= 200 && status < 300,
    status,
    redirected: options?.redirected || false,
    headers,
    json: () => Promise.resolve(body),
    text: () => Promise.resolve(JSON.stringify(body)),
  } as Response;
}

// ---------------------------------------------------------------------------
// checkAuthStatus
// ---------------------------------------------------------------------------

describe('checkAuthStatus', () => {
  it('returns true for 200 + JSON + no redirect', async () => {
    fetchMock.mockResolvedValue(makeResponse(200, { active: [] }));
    const result = await authService.checkAuthStatus();
    expect(result).toBe(true);
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/sessions/active', expect.objectContaining({
      method: 'GET',
      credentials: 'include',
    }));
  });

  it('returns false for redirected response', async () => {
    fetchMock.mockResolvedValue(makeResponse(200, {}, { redirected: true }));
    expect(await authService.checkAuthStatus()).toBe(false);
  });

  it('returns false for non-200 status', async () => {
    fetchMock.mockResolvedValue(makeResponse(401));
    expect(await authService.checkAuthStatus()).toBe(false);
  });

  it('returns false for non-JSON content-type', async () => {
    fetchMock.mockResolvedValue(
      makeResponse(200, '<html>', { headers: { 'content-type': 'text/html' } }),
    );
    expect(await authService.checkAuthStatus()).toBe(false);
  });

  it('returns false on network error', async () => {
    fetchMock.mockRejectedValue(new Error('network error'));
    expect(await authService.checkAuthStatus()).toBe(false);
  });

  it('returns false for 3xx status', async () => {
    fetchMock.mockResolvedValue(makeResponse(302));
    expect(await authService.checkAuthStatus()).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// getCurrentUser
// ---------------------------------------------------------------------------

describe('getCurrentUser', () => {
  it('gets user from userinfo endpoint', async () => {
    fetchMock.mockImplementation((url: string) => {
      if (url === '/oauth2/userinfo') {
        return Promise.resolve(makeResponse(200, { email: 'user@test.com', user: 'testuser' }));
      }
      return Promise.resolve(makeResponse(200, {}));
    });

    const user = await authService.getCurrentUser();
    expect(user).toEqual({
      email: 'user@test.com',
      name: 'testuser',
      groups: [],
    });
  });

  it('tries userinfo alternative fields', async () => {
    fetchMock.mockImplementation((url: string) => {
      if (url === '/oauth2/userinfo') {
        return Promise.resolve(makeResponse(200, { email: 'user@test.com', preferred_username: 'preferred' }));
      }
      return Promise.resolve(makeResponse(200, {}));
    });

    const user = await authService.getCurrentUser();
    expect(user?.name).toBe('preferred');
  });

  it('falls back to email prefix when no name fields', async () => {
    fetchMock.mockImplementation((url: string) => {
      if (url === '/oauth2/userinfo') {
        return Promise.resolve(makeResponse(200, { email: 'alice@example.com' }));
      }
      return Promise.resolve(makeResponse(200, {}));
    });

    const user = await authService.getCurrentUser();
    expect(user?.name).toBe('alice');
  });

  it('falls back to headers when userinfo fails', async () => {
    fetchMock.mockImplementation((url: string) => {
      if (url === '/oauth2/userinfo') {
        return Promise.reject(new Error('not available'));
      }
      if (url === '/api/v1/sessions/active') {
        return Promise.resolve(
          makeResponse(200, {}, {
            headers: {
              'content-type': 'application/json',
              'x-forwarded-email': 'user@test.com',
              'x-forwarded-user': 'headeruser',
            },
          }),
        );
      }
      return Promise.resolve(makeResponse(200, {}));
    });

    const user = await authService.getCurrentUser();
    expect(user?.email).toBe('user@test.com');
    expect(user?.name).toBe('headeruser');
  });

  it('uses email prefix when only email header present', async () => {
    fetchMock.mockImplementation((url: string) => {
      if (url === '/oauth2/userinfo') {
        return Promise.reject(new Error('not available'));
      }
      if (url === '/api/v1/sessions/active') {
        return Promise.resolve(
          makeResponse(200, {}, {
            headers: {
              'content-type': 'application/json',
              'x-forwarded-email': 'bob@example.com',
            },
          }),
        );
      }
      return Promise.resolve(makeResponse(200, {}));
    });

    const user = await authService.getCurrentUser();
    expect(user?.name).toBe('bob');
  });

  it('returns fallback user when no headers found', async () => {
    fetchMock.mockImplementation((url: string) => {
      if (url === '/oauth2/userinfo') {
        return Promise.reject(new Error('not available'));
      }
      return Promise.resolve(makeResponse(200, {}));
    });

    const user = await authService.getCurrentUser();
    expect(user?.email).toBe('unknown@user.com');
    expect(user?.name).toBe('Authenticated User');
  });

  it('returns null when all requests fail', async () => {
    fetchMock.mockRejectedValue(new Error('network error'));
    const user = await authService.getCurrentUser();
    expect(user).toBeNull();
  });

  it('returns null when fallback request is not ok', async () => {
    fetchMock.mockImplementation((url: string) => {
      if (url === '/oauth2/userinfo') {
        return Promise.reject(new Error('not available'));
      }
      return Promise.resolve(makeResponse(401));
    });
    const user = await authService.getCurrentUser();
    expect(user).toBeNull();
  });

  it('parses X-Forwarded-Groups header', async () => {
    fetchMock.mockImplementation((url: string) => {
      if (url === '/oauth2/userinfo') {
        return Promise.reject(new Error('not available'));
      }
      if (url === '/api/v1/sessions/active') {
        return Promise.resolve(
          makeResponse(200, {}, {
            headers: {
              'content-type': 'application/json',
              'x-forwarded-email': 'user@test.com',
              'x-forwarded-groups': 'admin, devops, ',
            },
          }),
        );
      }
      return Promise.resolve(makeResponse(200, {}));
    });

    const user = await authService.getCurrentUser();
    expect(user?.groups).toEqual(['admin', 'devops']);
  });
});

// ---------------------------------------------------------------------------
// handleAuthError
// ---------------------------------------------------------------------------

describe('handleAuthError', () => {
  it('redirects to login on normal path', () => {
    locationPathname = '/sessions/123';
    authService.handleAuthError();
    expect(locationHrefSetter).toHaveBeenCalledTimes(1);
    expect(locationHrefSetter.mock.calls[0][0]).toContain('/oauth2/sign_in');
  });

  it('does not redirect on /oauth2/sign_in', () => {
    locationPathname = '/oauth2/sign_in';
    authService.handleAuthError();
    expect(locationHrefSetter).not.toHaveBeenCalled();
  });

  it('does not redirect on /oauth2/callback', () => {
    locationPathname = '/oauth2/callback';
    authService.handleAuthError();
    expect(locationHrefSetter).not.toHaveBeenCalled();
  });
});

// ---------------------------------------------------------------------------
// redirectToLogin / logout
// ---------------------------------------------------------------------------

describe('redirectToLogin', () => {
  it('redirects with return URL', () => {
    locationPathname = '/sessions';
    locationSearch = '?status=failed';
    authService.redirectToLogin();
    expect(locationHrefSetter).toHaveBeenCalledTimes(1);
    const url = locationHrefSetter.mock.calls[0][0] as string;
    expect(url).toContain('/oauth2/sign_in?rd=');
    expect(url).toContain(encodeURIComponent('/sessions?status=failed'));
  });
});

describe('logout', () => {
  it('redirects to sign out', () => {
    authService.logout();
    expect(locationHrefSetter).toHaveBeenCalledTimes(1);
    expect(locationHrefSetter.mock.calls[0][0]).toContain('/oauth2/sign_out');
  });
});
