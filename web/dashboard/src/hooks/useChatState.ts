/**
 * useChatState — state management for the follow-up chat lifecycle.
 *
 * Manages: sendingMessage, canceling, chatStageId, error.
 * Does NOT subscribe to WebSocket events — SessionDetailPage drives state
 * transitions by calling onStageStarted / onStageTerminal from its
 * centralized WS handler.
 */

import { useState, useCallback, useRef, useEffect } from 'react';
import { sendChatMessage, cancelSession, handleAPIError } from '../services/api.ts';
import { TIMELINE_EVENT_TYPES } from '../constants/eventTypes.ts';
import type { TimelineEvent } from '../types/session.ts';

/** Safety timeout (ms) to clear sendingMessage if WS event never arrives. */
const SENDING_TIMEOUT_MS = 30_000;

/** Safety timeout (ms) to clear canceling if WS terminal event never arrives. */
const CANCEL_TIMEOUT_MS = 30_000;

export interface ChatState {
  sendingMessage: boolean;
  canceling: boolean;
  chatStageId: string | null;
  error: string | null;
}

export interface SendMessageResult {
  optimisticEvent: TimelineEvent;
  chatId: string;
  stageId: string;
}

export interface UseChatStateReturn extends ChatState {
  sendMessage: (content: string) => Promise<SendMessageResult | null>;
  cancelExecution: () => Promise<void>;
  onStageStarted: (stageId: string) => void;
  onStageTerminal: () => void;
  clearError: () => void;
}

export function useChatState(sessionId: string): UseChatStateReturn {
  const [sendingMessage, setSendingMessage] = useState(false);
  const [canceling, setCanceling] = useState(false);
  const [chatStageId, setChatStageId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const sendingTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const cancelTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  // Ref to track the current chatStageId for use in the safety timeout
  // callback, avoiding stale closure issues.
  const chatStageIdRef = useRef<string | null>(null);

  // Keep ref in sync with state
  useEffect(() => {
    chatStageIdRef.current = chatStageId;
  }, [chatStageId]);

  // Cleanup timeouts on unmount
  useEffect(() => {
    return () => {
      if (sendingTimeoutRef.current) {
        clearTimeout(sendingTimeoutRef.current);
      }
      if (cancelTimeoutRef.current) {
        clearTimeout(cancelTimeoutRef.current);
      }
    };
  }, []);

  const clearSendingTimeout = useCallback(() => {
    if (sendingTimeoutRef.current) {
      clearTimeout(sendingTimeoutRef.current);
      sendingTimeoutRef.current = null;
    }
  }, []);

  const clearCancelTimeout = useCallback(() => {
    if (cancelTimeoutRef.current) {
      clearTimeout(cancelTimeoutRef.current);
      cancelTimeoutRef.current = null;
    }
  }, []);

  const sendMessage = useCallback(async (content: string): Promise<SendMessageResult | null> => {
    setError(null);
    setSendingMessage(true);

    try {
      const response = await sendChatMessage(sessionId, content);

      setChatStageId(response.stage_id);
      // Sync ref immediately so a fast WS stage.status event (arriving
      // before React re-renders) can match the chat stage id.
      chatStageIdRef.current = response.stage_id;

      // Start safety timeout — if WS stage.status never arrives, clear
      // sendingMessage to avoid permanently stuck UI.
      clearSendingTimeout();
      sendingTimeoutRef.current = setTimeout(() => {
        console.warn('Chat processing timeout — clearing sending indicator');
        setSendingMessage(false);
        sendingTimeoutRef.current = null;
      }, SENDING_TIMEOUT_MS);

      // Build optimistic user_question TimelineEvent.
      // sequence_number is set to 0 here — the caller (SessionDetailPage)
      // patches it to max(existing) + 1 at injection time so the event
      // sorts correctly in parseTimelineToFlow.
      const now = new Date().toISOString();
      const optimisticEvent: TimelineEvent = {
        id: `temp-${Date.now()}`,
        session_id: sessionId,
        stage_id: response.stage_id,
        execution_id: null,
        sequence_number: 0,
        event_type: TIMELINE_EVENT_TYPES.USER_QUESTION,
        status: 'completed',
        content,
        metadata: null,
        created_at: now,
        updated_at: now,
      };

      return {
        optimisticEvent,
        chatId: response.chat_id,
        stageId: response.stage_id,
      };
    } catch (err) {
      setSendingMessage(false);
      setError(handleAPIError(err));
      return null;
    }
  }, [sessionId, clearSendingTimeout]);

  const cancelExecution = useCallback(async () => {
    setCanceling(true);
    clearCancelTimeout();
    cancelTimeoutRef.current = setTimeout(() => {
      console.warn('Chat cancel timeout — clearing canceling indicator');
      setCanceling(false);
      cancelTimeoutRef.current = null;
    }, CANCEL_TIMEOUT_MS);

    try {
      await cancelSession(sessionId);
    } catch (err) {
      setCanceling(false);
      clearCancelTimeout();
      setError(handleAPIError(err));
    }
  }, [sessionId, clearCancelTimeout]);

  // Called by SessionDetailPage WS handler when stage.status started arrives
  // for the chat stage.
  const onStageStarted = useCallback((stageId: string) => {
    // Only react if this matches our tracked chat stage
    if (chatStageIdRef.current && stageId === chatStageIdRef.current) {
      setSendingMessage(false);
      clearSendingTimeout();
    }
  }, [clearSendingTimeout]);

  // Called by SessionDetailPage WS handler when stage.status reaches
  // a terminal state for the chat stage.
  const onStageTerminal = useCallback(() => {
    setChatStageId(null);
    setSendingMessage(false);
    setCanceling(false);
    clearSendingTimeout();
    clearCancelTimeout();
  }, [clearSendingTimeout, clearCancelTimeout]);

  const clearError = useCallback(() => {
    setError(null);
  }, []);

  return {
    sendingMessage,
    canceling,
    chatStageId,
    error,
    sendMessage,
    cancelExecution,
    onStageStarted,
    onStageTerminal,
    clearError,
  };
}
