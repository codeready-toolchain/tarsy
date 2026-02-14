import { createContext, useContext, useEffect, useState, type ReactNode } from 'react';
import { authService, type AuthUser } from '../services/auth.ts';

export interface AuthState {
  user: AuthUser | null;
  isAuthenticated: boolean;
  isLoading: boolean;
  /** Whether oauth2-proxy auth is available. When false, all auth UI is hidden. */
  authAvailable: boolean;
}

const AuthContext = createContext<AuthState>({
  user: null,
  isAuthenticated: false,
  isLoading: true,
  authAvailable: false,
});

/**
 * Auth provider with graceful degradation.
 *
 * On mount, checks if oauth2-proxy is configured by probing the auth status.
 * If the check succeeds (200 + JSON), auth is available and user info is fetched.
 * If it fails with a network error or non-JSON response, auth is considered
 * unavailable and all auth UI elements are hidden — the dashboard works without login.
 */
export function AuthProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<AuthState>({
    user: null,
    isAuthenticated: false,
    isLoading: true,
    authAvailable: false,
  });

  useEffect(() => {
    let cancelled = false;

    async function checkAuth() {
      try {
        const isAuthenticated = await authService.checkAuthStatus();

        if (cancelled) return;

        if (isAuthenticated) {
          const user = await authService.getCurrentUser();
          if (cancelled) return;

          setState({
            user,
            isAuthenticated: true,
            isLoading: false,
            authAvailable: true,
          });
        } else {
          // Auth endpoint responded but user is not authenticated.
          // This means oauth2-proxy is configured but user needs to log in.
          // However, we also reach here when no auth proxy is configured
          // and the endpoint just returns JSON without auth.
          // Heuristic: if we got a valid response, auth might be available.
          // We'll try getCurrentUser — if it returns null, no auth UI.
          const user = await authService.getCurrentUser();
          if (cancelled) return;

          if (user) {
            setState({
              user,
              isAuthenticated: true,
              isLoading: false,
              authAvailable: true,
            });
          } else {
            // No auth configured — graceful degradation: hide auth UI
            setState({
              user: null,
              isAuthenticated: false,
              isLoading: false,
              authAvailable: false,
            });
          }
        }
      } catch {
        if (cancelled) return;
        // Network error or auth not available — graceful degradation
        setState({
          user: null,
          isAuthenticated: false,
          isLoading: false,
          authAvailable: false,
        });
      }
    }

    checkAuth();
    return () => {
      cancelled = true;
    };
  }, []);

  return <AuthContext value={state}>{children}</AuthContext>;
}

// eslint-disable-next-line react-refresh/only-export-components
export function useAuth(): AuthState {
  return useContext(AuthContext);
}
