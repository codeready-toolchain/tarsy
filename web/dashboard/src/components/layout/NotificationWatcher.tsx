import { useEffect, useRef } from 'react';
import { useNavigate } from 'react-router-dom';
import { useNotificationSettings } from '../../contexts/NotificationSettingsContext.tsx';
import { sessionDetailPath } from '../../constants/routes.ts';
import { getSessions } from '../../services/api.ts';
import { websocketService } from '../../services/websocket.ts';
import type { DashboardSessionItem } from '../../types/session.ts';
import {
  QUALIFIER_MAX_PAGES,
  needsAnotherPage,
  selectToastCandidates,
  shouldRefreshSessionEvent,
  type QualifierRow,
} from '../../utils/notificationDecision.ts';
import { showSessionNotification, notificationPermission } from '../../utils/notificationPermission.ts';
import {
  claimSeenId,
  loadSeenIds,
  qualifierParams,
  rememberSeenIds,
} from '../../utils/notificationSettings.ts';
import { applyTabIndicator } from '../../utils/tabIndicator.ts';

const REFRESH_MS = 1000;

function toRow(session: DashboardSessionItem): QualifierRow {
  return {
    id: session.id,
    alert_type: session.alert_type,
    labels: session.labels,
    executive_summary: session.executive_summary,
  };
}

/**
 * App-shell watcher. Runs while the master switch is on and at least one
 * signal is on, including on a session page.
 */
export function NotificationWatcher() {
  const { settings, setBacklogCount } = useNotificationSettings();
  const navigate = useNavigate();
  const navigateRef = useRef(navigate);
  useEffect(() => {
    navigateRef.current = navigate;
  }, [navigate]);

  const armed = settings.master && (settings.tabCounter || settings.desktop);
  const showCounter = armed && settings.tabCounter;
  const { desktop, labels } = settings;

  useEffect(() => {
    if (armed && showCounter) return;
    applyTabIndicator(null);
    setBacklogCount(null);
  }, [armed, showCounter, setBacklogCount]);

  useEffect(() => {
    if (!armed) return;

    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let previousTotal: number | null = null;
    let baseline = true;
    let inFlight: Promise<void> | null = null;
    let rerun = false;
    let unsubChannel = () => {};
    let unsubConn = () => {};

    const applyCount = (total: number) => {
      if (showCounter) {
        applyTabIndicator(total);
        setBacklogCount(total);
      } else {
        applyTabIndicator(null);
        setBacklogCount(null);
      }
    };

    const runRefresh = async () => {
      const isBaseline = baseline;
      try {
        const request = qualifierParams(labels);
        if (cancelled) return;
        if (request.kind === 'empty') {
          previousTotal = 0;
          baseline = false;
          applyCount(0);
          return;
        }

        const first = await getSessions({ ...request.params, page: 1 });
        if (cancelled) return;
        let rows = first.sessions;
        const total = first.pagination.total_items;
        let page = 1;
        const seen = new Set(loadSeenIds());
        while (needsAnotherPage({
          baseline: isBaseline,
          page,
          maxPages: QUALIFIER_MAX_PAGES,
          totalPages: first.pagination.total_pages,
          previousTotal,
          total,
          unseenOnFetchedPages: rows.filter((row) => !seen.has(row.id)).length,
        })) {
          page += 1;
          const next = await getSessions({ ...request.params, page });
          if (cancelled) return;
          rows = rows.concat(next.sessions);
        }

        previousTotal = total;
        baseline = false;
        applyCount(total);

        const qualifierRows = rows.map(toRow);
        if (!isBaseline) {
          const candidates = selectToastCandidates({
            baseline: false,
            desktop,
            hidden: document.hidden,
            permission: notificationPermission(),
            seen,
            rows: qualifierRows,
          });
          for (const candidate of candidates) {
            if (!claimSeenId(candidate.id)) continue;
            showSessionNotification(candidate, (id) => {
              window.focus();
              navigateRef.current(sessionDetailPath(id));
            });
          }
        }
        rememberSeenIds(qualifierRows.map((row) => row.id));
      } catch {
        // Keep the previous count and seen set until the next wake-up.
      }
    };

    const refresh = (): Promise<void> => {
      if (cancelled) return Promise.resolve();
      if (inFlight) {
        rerun = true;
        return inFlight;
      }
      const run = (async () => {
        do {
          rerun = false;
          await runRefresh();
        } while (rerun && !cancelled);
      })();
      inFlight = run.finally(() => {
        inFlight = null;
        if (rerun && !cancelled) void refresh();
      });
      return inFlight;
    };

    const schedule = () => {
      if (timer) clearTimeout(timer);
      timer = setTimeout(() => {
        void refresh();
      }, REFRESH_MS);
    };

    const arm = async () => {
      if (cancelled) return;
      unsubChannel = websocketService.subscribeToChannel('sessions', (data) => {
        if (shouldRefreshSessionEvent(data)) schedule();
      });
      unsubConn = websocketService.onConnectionChange((connected) => {
        if (connected) schedule();
      });
      websocketService.connect();
      await refresh();
    };

    void arm();

    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
      unsubChannel();
      unsubConn();
    };
  }, [armed, showCounter, desktop, labels, setBacklogCount]);

  return null;
}
