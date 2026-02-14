/**
 * Authentication service for OAuth2 Proxy integration.
 *
 * Handles auth status checking, user info retrieval, login/logout redirects,
 * and 401 error handling. Adapted from the old TARSy dashboard auth service.
 *
 * Graceful degradation: when oauth2-proxy is not configured, all auth checks
 * fail silently and auth UI is hidden.
 */

import { config } from '../config/env.ts';

export interface AuthUser {
  email: string;
  name?: string;
  groups?: string[];
}

class AuthService {
  private static instance: AuthService;

  static getInstance(): AuthService {
    if (!AuthService.instance) {
      AuthService.instance = new AuthService();
    }
    return AuthService.instance;
  }

  /**
   * Check if the user is authenticated by making a request to a protected endpoint.
   * Returns true only if: 200 status + no redirect + JSON response.
   */
  async checkAuthStatus(): Promise<boolean> {
    try {
      const response = await fetch('/api/v1/sessions/active', {
        method: 'GET',
        credentials: 'include',
        headers: { Accept: 'application/json' },
      });

      const isRedirected =
        response.redirected || (response.status >= 300 && response.status < 400);
      const contentType = response.headers.get('content-type');
      const isJsonResponse = Boolean(contentType && contentType.includes('application/json'));

      return response.status === 200 && !isRedirected && isJsonResponse;
    } catch (error) {
      if (config.isDevelopment) {
        console.warn('Auth status check failed:', error);
      }
      return false;
    }
  }

  /**
   * Get current user info from oauth2-proxy userinfo endpoint,
   * falling back to forwarded headers from a protected API call.
   */
  async getCurrentUser(): Promise<AuthUser | null> {
    try {
      // Try oauth2-proxy userinfo endpoint first
      try {
        const userinfoResponse = await fetch('/oauth2/userinfo', {
          method: 'GET',
          credentials: 'include',
          headers: { Accept: 'application/json' },
        });

        if (userinfoResponse.ok) {
          const userinfo = await userinfoResponse.json();
          if (userinfo.email) {
            return {
              email: userinfo.email,
              name:
                userinfo.user ||
                userinfo.login ||
                userinfo.preferred_username ||
                userinfo.name ||
                userinfo.email.split('@')[0],
              groups: [],
            };
          }
        }
      } catch {
        // oauth2-proxy not available, fall through to header fallback
      }

      // Fallback: extract user info from response headers
      const response = await fetch('/api/v1/sessions/active', {
        method: 'GET',
        credentials: 'include',
        headers: { Accept: 'application/json' },
      });

      if (!response.ok) {
        return null;
      }

      const rawEmail =
        response.headers.get('X-Forwarded-Email') || response.headers.get('X-User-Email');
      const email = rawEmail && rawEmail.includes('@') ? rawEmail : null;
      const username =
        response.headers.get('X-Forwarded-User') ||
        response.headers.get('X-Forwarded-Preferred-Username') ||
        response.headers.get('X-User-Name');

      if (email) {
        return {
          email,
          name: username || email.split('@')[0],
          groups:
            response.headers
              .get('X-Forwarded-Groups')
              ?.split(',')
              .map((g) => g.trim())
              .filter((g) => g) || [],
        };
      }

      if (username) {
        return {
          email: 'unknown@user.com',
          name: username,
          groups: [],
        };
      }

      // Authenticated but no user info available
      return {
        email: 'unknown@user.com',
        name: 'Authenticated User',
        groups: [],
      };
    } catch {
      return null;
    }
  }

  /** Redirect to OAuth2 proxy login page. */
  redirectToLogin(): void {
    const currentPath = window.location.pathname + window.location.search;
    const returnUrl = `${window.location.origin}${currentPath}`;
    window.location.href = `/oauth2/sign_in?rd=${encodeURIComponent(returnUrl)}`;
  }

  /** Logout by clearing OAuth session. */
  logout(): void {
    const redirectUrl = encodeURIComponent(window.location.origin + '/');
    window.location.href = `/oauth2/sign_out?rd=${redirectUrl}`;
  }

  /**
   * Handle authentication error (401) by redirecting to login.
   * Prevents redirect loops on OAuth2 proxy pages.
   */
  handleAuthError(): void {
    const currentPath = window.location.pathname;
    if (currentPath.startsWith('/oauth2/sign_in') || currentPath.startsWith('/oauth2/callback')) {
      return;
    }
    this.redirectToLogin();
  }
}

export const authService = AuthService.getInstance();
