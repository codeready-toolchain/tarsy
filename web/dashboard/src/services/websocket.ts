/**
 * WebSocket service — single connection with channel subscriptions.
 *
 * Adapted from the old TARSy dashboard WebSocket service for the new protocol:
 * - Channel model: `sessions` (global), `session:{id}` (per-session)
 * - Actions: subscribe, unsubscribe, catchup (with last_event_id), ping
 * - Auto-catchup on subscribe (server sends prior events)
 * - Reconnect with exponential backoff (200ms → 3s cap, never give up)
 * - Keepalive ping/pong (20s interval, 10s pong timeout)
 * - catchup.overflow handling (signals full REST reload needed)
 */

import { urls } from '../config/env.ts';
import { EVENT_PONG, EVENT_CATCHUP_OVERFLOW } from '../constants/eventTypes.ts';

type EventHandler = (data: Record<string, unknown>) => void;

interface SubscribedChannel {
  channel: string;
  lastEventId: number;
  handlers: EventHandler[];
}

class WebSocketService {
  private ws: WebSocket | null = null;
  private reconnectAttempts = 0;
  private reconnectTimeout: ReturnType<typeof setTimeout> | null = null;
  private isConnecting = false;

  // Channel subscriptions
  private channels: Map<string, SubscribedChannel> = new Map();

  // Global event-type handlers
  private eventHandlers: Map<string, EventHandler[]> = new Map();
  private connectionHandlers: Array<(connected: boolean) => void> = [];

  // Keepalive
  private pingInterval: ReturnType<typeof setInterval> | null = null;
  private pongTimeout: ReturnType<typeof setTimeout> | null = null;
  private readonly PING_INTERVAL_MS = 20_000;
  private readonly PONG_TIMEOUT_MS = 10_000;

  // ────────────────────────────────────────────────────────────
  // Connection lifecycle
  // ────────────────────────────────────────────────────────────

  connect(): void {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      return;
    }
    if (this.isConnecting) {
      return;
    }

    this.isConnecting = true;

    try {
      const wsUrl = this.buildWsUrl();
      this.ws = new WebSocket(wsUrl);

      this.ws.onopen = () => {
        this.isConnecting = false;
        this.reconnectAttempts = 0;
        this.notifyConnectionChange(true);
        this.startKeepalive();
        this.resubscribeAll();
      };

      this.ws.onmessage = (event) => {
        try {
          const data = JSON.parse(event.data);
          if (data.type === EVENT_PONG) {
            this.handlePong();
            return;
          }
          this.handleEvent(data);
        } catch {
          // Ignore malformed messages
        }
      };

      this.ws.onerror = () => {
        this.isConnecting = false;
      };

      this.ws.onclose = () => {
        this.stopKeepalive();
        this.ws = null;
        this.isConnecting = false;
        this.notifyConnectionChange(false);
        this.scheduleReconnect();
      };
    } catch {
      this.isConnecting = false;
      this.scheduleReconnect();
    }
  }

  disconnect(): void {
    this.stopKeepalive();
    if (this.reconnectTimeout) {
      clearTimeout(this.reconnectTimeout);
      this.reconnectTimeout = null;
    }
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
  }

  get isConnected(): boolean {
    return this.ws !== null && this.ws.readyState === WebSocket.OPEN;
  }

  // ────────────────────────────────────────────────────────────
  // Channel subscriptions
  // ────────────────────────────────────────────────────────────

  /**
   * Subscribe to a channel with a handler.
   * Returns an unsubscribe function.
   */
  subscribeToChannel(channel: string, handler: EventHandler): () => void {
    if (!this.channels.has(channel)) {
      this.channels.set(channel, {
        channel,
        lastEventId: 0,
        handlers: [],
      });
      this.sendSubscribe(channel);
    }

    const sub = this.channels.get(channel)!;
    sub.handlers.push(handler);

    return () => {
      const sub = this.channels.get(channel);
      if (sub) {
        const index = sub.handlers.indexOf(handler);
        if (index > -1) {
          sub.handlers.splice(index, 1);
        }
        if (sub.handlers.length === 0) {
          this.sendUnsubscribe(channel);
          this.channels.delete(channel);
        }
      }
    };
  }

  /**
   * Listen for a specific event type globally (across all channels).
   * Returns an unsubscribe function.
   */
  onEvent(eventType: string, handler: EventHandler): () => void {
    if (!this.eventHandlers.has(eventType)) {
      this.eventHandlers.set(eventType, []);
    }
    this.eventHandlers.get(eventType)!.push(handler);

    return () => {
      const handlers = this.eventHandlers.get(eventType);
      if (handlers) {
        const index = handlers.indexOf(handler);
        if (index > -1) {
          handlers.splice(index, 1);
        }
      }
    };
  }

  /** Listen for connection state changes. Returns an unsubscribe function. */
  onConnectionChange(handler: (connected: boolean) => void): () => void {
    this.connectionHandlers.push(handler);
    return () => {
      const index = this.connectionHandlers.indexOf(handler);
      if (index > -1) {
        this.connectionHandlers.splice(index, 1);
      }
    };
  }

  // ────────────────────────────────────────────────────────────
  // Internal: URL building
  // ────────────────────────────────────────────────────────────

  private buildWsUrl(): string {
    const base = urls.websocket.base;
    if (!base || base === '') {
      // Production same-origin: derive from page location
      const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
      return `${protocol}//${window.location.host}${urls.websocket.path}`;
    }
    return `${base}${urls.websocket.path}`;
  }

  // ────────────────────────────────────────────────────────────
  // Internal: message sending
  // ────────────────────────────────────────────────────────────

  private send(data: object): void {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(data));
    }
  }

  private sendSubscribe(channel: string): void {
    this.send({ action: 'subscribe', channel });
  }

  private sendUnsubscribe(channel: string): void {
    this.send({ action: 'unsubscribe', channel });
  }

  private sendCatchup(channel: string, lastEventId: number): void {
    this.send({ action: 'catchup', channel, last_event_id: lastEventId });
  }

  // ────────────────────────────────────────────────────────────
  // Internal: event routing
  // ────────────────────────────────────────────────────────────

  private handleEvent(data: Record<string, unknown>): void {
    const eventType = data.type as string | undefined;
    const sessionId = data.session_id as string | undefined;

    // Handle catchup overflow — emit to all channel handlers
    if (eventType === EVENT_CATCHUP_OVERFLOW) {
      for (const sub of this.channels.values()) {
        sub.handlers.forEach((h) => h(data));
      }
      return;
    }

    // Track last event ID for catchup on reconnect.
    // The backend injects db_event_id into persisted event payloads (both
    // NOTIFY and catchup). Transient events (stream.chunk, etc.) don't have
    // db_event_id, which is correct — they're not in the DB and can't be
    // caught up on.
    const eventId = data.db_event_id as number | undefined;
    if (eventId && sessionId) {
      const sessionChannel = this.channels.get(`session:${sessionId}`);
      if (sessionChannel) {
        sessionChannel.lastEventId = eventId;
      }
    }
    if (eventId && eventType?.startsWith('session.')) {
      const globalChannel = this.channels.get('sessions');
      if (globalChannel) {
        globalChannel.lastEventId = eventId;
      }
    }

    // Route to session-specific channel handlers
    if (sessionId) {
      const sessionChannel = this.channels.get(`session:${sessionId}`);
      if (sessionChannel) {
        sessionChannel.handlers.forEach((h) => h(data));
      }
    }

    // Route to global `sessions` channel handlers for session-level events
    if (eventType?.startsWith('session.')) {
      const globalChannel = this.channels.get('sessions');
      if (globalChannel) {
        globalChannel.handlers.forEach((h) => h(data));
      }
    }

    // Route to global event-type handlers
    if (eventType) {
      const handlers = this.eventHandlers.get(eventType);
      if (handlers) {
        handlers.forEach((h) => h(data));
      }
    }
  }

  // ────────────────────────────────────────────────────────────
  // Internal: reconnect
  // ────────────────────────────────────────────────────────────

  private scheduleReconnect(): void {
    this.reconnectAttempts++;
    // Exponential backoff: 200ms, 400ms, 800ms, 1.6s, capped at 3s
    const delay = Math.min(200 * Math.pow(2, this.reconnectAttempts - 1), 3000);
    this.reconnectTimeout = setTimeout(() => {
      this.connect();
    }, delay);
  }

  private resubscribeAll(): void {
    for (const [channel, sub] of this.channels.entries()) {
      this.sendSubscribe(channel);
      if (sub.lastEventId > 0) {
        this.sendCatchup(channel, sub.lastEventId);
      }
    }
  }

  // ────────────────────────────────────────────────────────────
  // Internal: keepalive (ping/pong)
  // ────────────────────────────────────────────────────────────

  private startKeepalive(): void {
    this.stopKeepalive();
    this.sendPing();
    this.pingInterval = setInterval(() => {
      this.sendPing();
    }, this.PING_INTERVAL_MS);
  }

  private stopKeepalive(): void {
    if (this.pingInterval) {
      clearInterval(this.pingInterval);
      this.pingInterval = null;
    }
    if (this.pongTimeout) {
      clearTimeout(this.pongTimeout);
      this.pongTimeout = null;
    }
  }

  private sendPing(): void {
    this.send({ action: 'ping' });
    this.pongTimeout = setTimeout(() => {
      // No pong received — connection is stale, close to trigger reconnect
      if (this.ws) {
        this.ws.close();
      }
    }, this.PONG_TIMEOUT_MS);
  }

  private handlePong(): void {
    if (this.pongTimeout) {
      clearTimeout(this.pongTimeout);
      this.pongTimeout = null;
    }
  }

  // ────────────────────────────────────────────────────────────
  // Internal: connection state
  // ────────────────────────────────────────────────────────────

  private notifyConnectionChange(connected: boolean): void {
    this.connectionHandlers.forEach((h) => h(connected));
  }
}

/** Singleton WebSocket service instance. */
export const websocketService = new WebSocketService();
