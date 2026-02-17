/**
 * Version monitoring hook.
 *
 * Polls backend version from /health every 30s and checks index.html meta tag
 * for dashboard version changes. Shows update banner after 2 consecutive
 * mismatches (~60s) to avoid flicker during rolling updates.
 *
 * Ported from old TARSy dashboard, adapted for new API patterns.
 */

import { useState, useEffect, useCallback, useRef } from 'react';
import { getHealth } from '../services/api.ts';
import { DASHBOARD_VERSION } from '../config/env.ts';

const POLL_INTERVAL_MS = 30_000; // 30 seconds
const REQUIRED_CONSECUTIVE_MISMATCHES = 2; // 60 seconds total before banner

/** Version monitoring state exposed by the hook. */
export interface VersionInfo {
  /** Current backend version from latest poll. */
  backendVersion: string | null;
  /** Backend health status from latest poll. */
  backendStatus: string;
  /** Whether dashboard version has changed (requires 2 consecutive mismatches). */
  dashboardVersionChanged: boolean;
  /** Manually refresh version info. */
  refresh: () => Promise<void>;
}

export function useVersionMonitor(): VersionInfo {
  const [backendVersion, setBackendVersion] = useState<string | null>(null);
  const [backendStatus, setBackendStatus] = useState<string>('checking');

  const [, setConsecutiveMismatches] = useState(0);
  const [dashboardVersionChanged, setDashboardVersionChanged] = useState(false);

  const isInitialMount = useRef(true);

  const fetchBackendVersion = useCallback(async () => {
    try {
      const health = await getHealth();
      setBackendVersion(health.version || 'unknown');
      setBackendStatus(health.status || 'unknown');
    } catch (error) {
      console.error('Failed to fetch backend version:', error);
      setBackendStatus('error');
    }
  }, []);

  const checkDashboardVersion = useCallback(async () => {
    try {
      const response = await fetch(`/index.html?_=${Date.now()}`, {
        cache: 'no-cache',
        headers: { 'Cache-Control': 'no-cache' },
      });

      if (!response.ok) return;

      const html = await response.text();
      const versionMatch = html.match(
        /<meta\s+name=["']app-version["']\s+content=["']([^"']+)["']/i,
      );
      const fetchedVersion = versionMatch?.[1];

      // Skip if version not injected (dev mode) or empty
      if (!fetchedVersion || fetchedVersion === '%VITE_APP_VERSION%') {
        return;
      }

      if (fetchedVersion !== DASHBOARD_VERSION) {
        setConsecutiveMismatches((prev) => {
          const newCount = prev + 1;
          if (newCount >= REQUIRED_CONSECUTIVE_MISMATCHES && !isInitialMount.current) {
            setDashboardVersionChanged(true);
          }
          return newCount;
        });
      } else {
        setConsecutiveMismatches((prev) => (prev > 0 ? 0 : prev));
      }
    } catch {
      // Silently fail — optional monitoring
    }
  }, []);

  const refresh = useCallback(async () => {
    await Promise.all([fetchBackendVersion(), checkDashboardVersion()]);
  }, [fetchBackendVersion, checkDashboardVersion]);

  // Initial fetch on mount
  useEffect(() => {
    refresh();

    // Mark initial mount complete after first poll cycle
    const timer = setTimeout(() => {
      isInitialMount.current = false;
    }, 1000);

    return () => clearTimeout(timer);
  }, [refresh]);

  // Polling interval
  useEffect(() => {
    const id = setInterval(refresh, POLL_INTERVAL_MS);
    return () => clearInterval(id);
  }, [refresh]);

  return { backendVersion, backendStatus, dashboardVersionChanged, refresh };
}
