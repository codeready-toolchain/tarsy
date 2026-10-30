import { act, render } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { EVENT_SESSION_STATUS } from '../../constants/eventTypes';
import { SESSION_STATUS } from '../../constants/sessionStatus';
import { NotificationSettingsProvider } from '../../contexts/NotificationSettingsContext';
import type { DashboardListResponse } from '../../types/api';

const ws = vi.hoisted(() => ({
  fetchStarted: false,
  subscribedBeforeFetch: false,
  connectedBeforeFetch: false,
  handler: null as ((data: Record<string, unknown>) => void) | null,
}));

vi.mock('../../services/api.ts', () => ({
  getSessions: vi.fn(),
}));

vi.mock('../../services/websocket.ts', () => ({
  websocketService: {
    subscribeToChannel: (_channel: string, handler: (data: Record<string, unknown>) => void) => {
      ws.subscribedBeforeFetch = !ws.fetchStarted;
      ws.handler = handler;
      return () => {
        ws.handler = null;
      };
    },
    onConnectionChange: () => () => {},
    connect: () => {
      ws.connectedBeforeFetch = !ws.fetchStarted;
    },
  },
}));

import { NotificationWatcher } from '../../components/layout/NotificationWatcher';
import { getSessions } from '../../services/api';

const mockGetSessions = vi.mocked(getSessions);

function listWith(total: number): DashboardListResponse {
  return {
    sessions: [],
    pagination: { page: 1, page_size: 100, total_items: total, total_pages: 1 },
  };
}

const emptyList = listWith(0);

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

describe('NotificationWatcher', () => {
  beforeEach(() => {
    localStorage.clear();
    document.title = 'TARSy';
    ws.fetchStarted = false;
    ws.subscribedBeforeFetch = false;
    ws.connectedBeforeFetch = false;
    ws.handler = null;
    mockGetSessions.mockReset();
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('hears a completion that arrives while the first list request is open', async () => {
    const first = deferred<DashboardListResponse>();
    mockGetSessions.mockImplementation(() => {
      ws.fetchStarted = true;
      return first.promise;
    });

    render(
      <MemoryRouter>
        <NotificationSettingsProvider>
          <NotificationWatcher />
        </NotificationSettingsProvider>
      </MemoryRouter>,
    );

    await act(async () => {
      await Promise.resolve();
    });

    expect(ws.subscribedBeforeFetch).toBe(true);
    expect(ws.connectedBeforeFetch).toBe(true);
    expect(mockGetSessions).toHaveBeenCalledTimes(1);

    act(() => {
      ws.handler?.({ type: EVENT_SESSION_STATUS, status: SESSION_STATUS.COMPLETED });
    });

    mockGetSessions.mockResolvedValue(emptyList);
    await act(async () => {
      first.resolve(emptyList);
      await first.promise;
    });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });

    expect(mockGetSessions).toHaveBeenCalledTimes(2);
  });

  it('keeps the newer list when an older request finishes last', async () => {
    const slow = deferred<DashboardListResponse>();
    mockGetSessions.mockImplementation(() => {
      if (mockGetSessions.mock.calls.length === 1) return slow.promise;
      return Promise.resolve(listWith(7));
    });

    render(
      <MemoryRouter>
        <NotificationSettingsProvider>
          <NotificationWatcher />
        </NotificationSettingsProvider>
      </MemoryRouter>,
    );

    await act(async () => {
      await Promise.resolve();
    });

    act(() => {
      ws.handler?.({ type: EVENT_SESSION_STATUS, status: SESSION_STATUS.COMPLETED });
    });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    expect(document.title).toBe('(7) TARSy');

    await act(async () => {
      slow.resolve(listWith(1));
      await slow.promise;
    });
    expect(document.title).toBe('(7) TARSy');
  });
});
