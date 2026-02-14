/**
 * Environment Configuration
 *
 * Development: relative API URLs via Vite proxy, WebSocket direct to Go backend
 * Production: absolute URLs from environment or relative (same-origin serving)
 */

interface AppConfig {
  isDevelopment: boolean;
  isProduction: boolean;
}

const parseEnvConfig = (): AppConfig => ({
  isDevelopment: import.meta.env.DEV,
  isProduction: import.meta.env.PROD,
});

export const config = parseEnvConfig();

/**
 * Dashboard version from build-time environment variable.
 * Set via VITE_APP_VERSION in .env or CI build.
 */
export const DASHBOARD_VERSION: string = import.meta.env.VITE_APP_VERSION || 'dev';

/**
 * Resolve WebSocket base URL.
 * - Development: direct to Go backend (bypass Vite proxy — WS upgrades need explicit URL)
 * - Production with explicit VITE_WS_BASE_URL: use that
 * - Production without: derive from page location (same-origin)
 */
function resolveWsBaseUrl(): string {
  if (config.isDevelopment) {
    return 'ws://localhost:8080';
  }

  const explicitWsUrl = import.meta.env.VITE_WS_BASE_URL;
  if (explicitWsUrl !== undefined && explicitWsUrl !== null && explicitWsUrl !== '') {
    return explicitWsUrl;
  }

  // Production same-origin: derive from page location
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${protocol}//${window.location.host}`;
}

/**
 * URL configuration — single source of truth.
 * Development: relative API URLs (Vite proxy handles forwarding to Go backend)
 * Production: from env vars or same-origin
 */
export const urls = {
  api: {
    // Dev: empty (relative, proxied by Vite). Prod: from env or empty (same-origin).
    base: config.isDevelopment ? '' : (import.meta.env.VITE_API_BASE_URL ?? ''),
    health: '/health',
  },

  websocket: {
    base: resolveWsBaseUrl(),
    path: '/api/v1/ws',
  },

  oauth: {
    signIn: '/oauth2/sign_in',
    signOut: '/oauth2/sign_out',
    userInfo: '/oauth2/userinfo',
  },
} as const;
