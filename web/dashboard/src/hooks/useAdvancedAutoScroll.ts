import { useEffect, useRef, useCallback } from 'react';

export interface AdvancedAutoScrollOptions {
  /** Whether auto-scroll is enabled (only for active sessions) */
  enabled?: boolean;
  /** Pixels from bottom to consider "at bottom" */
  threshold?: number;
  /** Delay before auto-scrolling after content change (ms) */
  scrollDelay?: number;
  /** CSS selector for the container to observe */
  observeSelector?: string;
}

interface AutoScrollState {
  isUserAtBottom: boolean;
  userScrolledAway: boolean;
  isAutoScrolling: boolean;
}

/**
 * useAdvancedAutoScroll — MutationObserver-based auto-scroll hook.
 *
 * Watches [data-autoscroll-container] for DOM changes and scrolls the window
 * to the bottom unless the user has explicitly scrolled away.
 * Enabled only for active sessions and disables 2 seconds after the session
 * transitions to a terminal state.
 */
export function useAdvancedAutoScroll(options: AdvancedAutoScrollOptions = {}) {
  const {
    enabled = true,
    threshold = 10,
    scrollDelay = 300,
    observeSelector = '[data-autoscroll-container]',
  } = options;

  const stateRef = useRef<AutoScrollState>({
    isUserAtBottom: true,
    userScrolledAway: false,
    isAutoScrolling: false,
  });

  const mutationObserverRef = useRef<MutationObserver | null>(null);
  const scrollTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const userScrollTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const rafIdRef = useRef<number | null>(null);
  const autoScrollMonitorRafRef = useRef<number | null>(null);
  const autoScrollStartTimeRef = useRef<number | null>(null);
  const userInteractionRef = useRef(false);
  const clearUserInteractionTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const characterDataThrottleRef = useRef<number>(0);

  const isAtBottom = useCallback((): boolean => {
    const scrollTop = window.pageYOffset || document.documentElement.scrollTop;
    const windowHeight = window.innerHeight;
    const documentHeight = document.documentElement.scrollHeight;
    return documentHeight - scrollTop - windowHeight <= threshold;
  }, [threshold]);

  const scrollToBottom = useCallback((smooth = true) => {
    if (rafIdRef.current) cancelAnimationFrame(rafIdRef.current);
    if (autoScrollMonitorRafRef.current) {
      cancelAnimationFrame(autoScrollMonitorRafRef.current);
      autoScrollMonitorRafRef.current = null;
    }

    rafIdRef.current = requestAnimationFrame(() => {
      stateRef.current.isAutoScrolling = true;
      autoScrollStartTimeRef.current = performance.now();

      const target = document.documentElement.scrollHeight;
      if (smooth) {
        window.scrollTo({ top: target, behavior: 'smooth' });
      } else {
        window.scrollTo(0, target);
      }

      const monitor = () => {
        const now = performance.now();
        const atBottom = document.documentElement.scrollHeight - (window.pageYOffset || document.documentElement.scrollTop) - window.innerHeight <= threshold;
        if (atBottom) { stateRef.current.isAutoScrolling = false; autoScrollMonitorRafRef.current = null; return; }
        if (autoScrollStartTimeRef.current !== null && now - autoScrollStartTimeRef.current > 2000) { stateRef.current.isAutoScrolling = false; autoScrollMonitorRafRef.current = null; return; }
        autoScrollMonitorRafRef.current = requestAnimationFrame(monitor);
      };
      autoScrollMonitorRafRef.current = requestAnimationFrame(monitor);
    });
  }, [threshold]);

  const handleScroll = useCallback(() => {
    if (stateRef.current.isAutoScrolling) return;
    const wasAtBottom = stateRef.current.isUserAtBottom;
    const isNowAtBottom = isAtBottom();
    stateRef.current.isUserAtBottom = isNowAtBottom;

    if (wasAtBottom && !isNowAtBottom && userInteractionRef.current) {
      stateRef.current.userScrolledAway = true;
    } else if (!wasAtBottom && isNowAtBottom) {
      stateRef.current.userScrolledAway = false;
    }

    if (userScrollTimeoutRef.current) clearTimeout(userScrollTimeoutRef.current);
    userScrollTimeoutRef.current = setTimeout(() => {}, 1000);
  }, [isAtBottom]);

  const markUserInteraction = useCallback(() => {
    userInteractionRef.current = true;
    if (clearUserInteractionTimeoutRef.current) clearTimeout(clearUserInteractionTimeoutRef.current);
    clearUserInteractionTimeoutRef.current = setTimeout(() => { userInteractionRef.current = false; }, 1500);
  }, []);

  const handlePointerDown = useCallback(() => {
    userInteractionRef.current = true;
    if (clearUserInteractionTimeoutRef.current) { clearTimeout(clearUserInteractionTimeoutRef.current); clearUserInteractionTimeoutRef.current = null; }
  }, []);

  const handlePointerUp = useCallback(() => {
    if (clearUserInteractionTimeoutRef.current) clearTimeout(clearUserInteractionTimeoutRef.current);
    clearUserInteractionTimeoutRef.current = setTimeout(() => { userInteractionRef.current = false; }, 1500);
  }, []);

  const handleKeydown = useCallback((e: KeyboardEvent) => {
    if (['ArrowUp', 'ArrowDown', 'PageUp', 'PageDown', 'Home', 'End', ' ', 'Spacebar'].includes(e.key)) {
      markUserInteraction();
    }
  }, [markUserInteraction]);

  const tryAutoScroll = useCallback(() => {
    if (!enabled || stateRef.current.userScrolledAway) return;
    if (scrollTimeoutRef.current) clearTimeout(scrollTimeoutRef.current);
    scrollTimeoutRef.current = setTimeout(() => {
      if (enabled && !stateRef.current.userScrolledAway) scrollToBottom(true);
    }, scrollDelay);
  }, [enabled, scrollDelay, scrollToBottom]);

  const setupMutationObserver = useCallback(() => {
    if (!enabled) return;
    if (mutationObserverRef.current) mutationObserverRef.current.disconnect();

    const container = document.querySelector(observeSelector);
    if (!container) return;

    mutationObserverRef.current = new MutationObserver((mutations) => {
      let hasChildList = false;
      let hasCharData = false;
      for (const m of mutations) {
        if (m.type === 'childList' && m.addedNodes.length > 0) hasChildList = true;
        else if (m.type === 'characterData') hasCharData = true;
      }

      if (hasChildList) {
        tryAutoScroll();
      } else if (hasCharData) {
        const now = Date.now();
        if (now - characterDataThrottleRef.current >= 500) {
          characterDataThrottleRef.current = now;
          tryAutoScroll();
        }
      }
    });

    mutationObserverRef.current.observe(container, { childList: true, subtree: true, characterData: true });
  }, [enabled, observeSelector, tryAutoScroll]);

  useEffect(() => {
    if (!enabled) return;

    stateRef.current.isUserAtBottom = isAtBottom();
    stateRef.current.userScrolledAway = !stateRef.current.isUserAtBottom;

    window.addEventListener('scroll', handleScroll, { passive: true });
    window.addEventListener('wheel', markUserInteraction as EventListener, { passive: true });
    window.addEventListener('pointerdown', handlePointerDown as EventListener, { passive: true });
    window.addEventListener('pointerup', handlePointerUp as EventListener, { passive: true });
    window.addEventListener('keydown', handleKeydown as EventListener);

    setupMutationObserver();

    return () => {
      window.removeEventListener('scroll', handleScroll);
      window.removeEventListener('wheel', markUserInteraction as EventListener);
      window.removeEventListener('pointerdown', handlePointerDown as EventListener);
      window.removeEventListener('pointerup', handlePointerUp as EventListener);
      window.removeEventListener('keydown', handleKeydown as EventListener);
      if (mutationObserverRef.current) { mutationObserverRef.current.disconnect(); mutationObserverRef.current = null; }
      if (scrollTimeoutRef.current) clearTimeout(scrollTimeoutRef.current);
      if (userScrollTimeoutRef.current) clearTimeout(userScrollTimeoutRef.current);
      if (rafIdRef.current) cancelAnimationFrame(rafIdRef.current);
      if (autoScrollMonitorRafRef.current) cancelAnimationFrame(autoScrollMonitorRafRef.current);
      if (clearUserInteractionTimeoutRef.current) {
        clearTimeout(clearUserInteractionTimeoutRef.current);
        clearUserInteractionTimeoutRef.current = null;
      }
    };
  }, [enabled, handleScroll, markUserInteraction, handlePointerDown, handlePointerUp, handleKeydown, setupMutationObserver, isAtBottom]);

  useEffect(() => { setupMutationObserver(); }, [setupMutationObserver]);

  return {
    tryAutoScroll,
    scrollToBottom: () => scrollToBottom(true),
    getState: () => ({ ...stateRef.current }),
    isAtBottom,
  };
}
