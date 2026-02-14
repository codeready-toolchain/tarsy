/// <reference types="vitest" />
import { defineConfig, loadEnv } from 'vite';
import react from '@vitejs/plugin-react';

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '');

  // Development server configuration
  const devServerHost = env.VITE_DEV_SERVER_HOST || 'localhost';
  const devServerPort = parseInt(env.VITE_DEV_SERVER_PORT || '5173', 10);

  // Container mode: when running inside podman-compose with oauth2-proxy
  const isContainerMode =
    env.VITE_PROXY_TARGET_HTTP || env.NODE_ENV === 'container';

  // Proxy targets:
  // - Container mode: proxy to oauth2-proxy service
  // - Dev mode: proxy to direct Go backend (port 8080)
  const backendHttpTarget = isContainerMode
    ? env.VITE_PROXY_TARGET_HTTP || 'http://oauth2-proxy:4180'
    : 'http://localhost:8080';
  const proxyHostHeader = isContainerMode
    ? env.VITE_PROXY_HOST_HEADER || 'localhost:4180'
    : 'localhost:8080';

  return {
    plugins: [react()],
    server: {
      host: devServerHost,
      port: devServerPort,
      proxy: (() => {
        const configureContainerProxy = isContainerMode
          ? {
              configure: (proxy: unknown) => {
                (proxy as { on: (event: string, handler: (...args: unknown[]) => void) => void }).on(
                  'proxyReq',
                  (...args: unknown[]) => {
                    const proxyReq = args[0] as { setHeader: (name: string, value: string) => void };
                    const req = args[1] as { headers: { cookie?: string } };
                    if (req.headers.cookie) {
                      proxyReq.setHeader('Cookie', req.headers.cookie);
                    }
                    proxyReq.setHeader('Host', proxyHostHeader);
                  },
                );
              },
            }
          : {};

        const baseProxy: Record<string, object> = {
          '/api': {
            target: backendHttpTarget,
            changeOrigin: true,
            secure: false,
            ws: true,
            ...configureContainerProxy,
          },
          '/health': {
            target: backendHttpTarget,
            changeOrigin: true,
            secure: false,
            ...configureContainerProxy,
          },
        };

        if (isContainerMode) {
          baseProxy['/oauth2'] = {
            target: backendHttpTarget,
            changeOrigin: true,
            secure: false,
            ...configureContainerProxy,
          };
        }

        return baseProxy;
      })(),
    },
    test: {
      globals: true,
      environment: 'jsdom',
      setupFiles: ['./src/test/setup.ts'],
    },
  };
});
