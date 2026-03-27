/**
 * ChatPanel — always-visible follow-up chat input.
 *
 * Acts as a visual divider between ConversationTimeline and FinalAnalysisCard.
 * No header — the big icon + placeholder text communicate purpose.
 * Chat messages themselves render in the timeline, not here.
 */

import { forwardRef } from 'react';
import {
  Box,
  Paper,
  Typography,
  Alert,
  alpha,
} from '@mui/material';
import { Forum } from '@mui/icons-material';
import ChatInput from './ChatInput.tsx';

interface ChatPanelProps {
  isAvailable: boolean;
  chatExists: boolean;
  onSendMessage: (content: string) => void;
  onCancelExecution: () => void;
  sendingMessage?: boolean;
  chatStageInProgress?: boolean;
  canCancel?: boolean;
  canceling?: boolean;
  error?: string | null;
  onClearError?: () => void;
}

const ChatPanel = forwardRef<HTMLDivElement, ChatPanelProps>(function ChatPanel({
  isAvailable,
  chatExists,
  onSendMessage,
  onCancelExecution,
  sendingMessage = false,
  chatStageInProgress = false,
  canCancel = false,
  canceling = false,
  error,
  onClearError,
}, ref) {
  if (!isAvailable) return null;

  const inputDisabled = sendingMessage || chatStageInProgress;

  return (
    <Paper
      ref={ref}
      elevation={1}
      sx={(theme) => ({
        overflow: 'hidden',
        border: `2px solid ${theme.palette.primary.main}`,
      })}
    >
      {error && (
        <Alert severity="error" sx={{ m: 1.5, mb: 0 }} onClose={onClearError}>
          <Typography variant="body2">{error}</Typography>
        </Alert>
      )}

      {inputDisabled && (
        <Box
          sx={(theme) => ({
            height: 3,
            width: '100%',
            bgcolor: alpha(theme.palette.primary.main, 0.15),
          })}
        />
      )}

      <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: 1.5, p: 2, pb: 0 }}>
        {/* Big circle icon — same style as old header */}
        <Box
          sx={(theme) => ({
            width: 40,
            height: 40,
            borderRadius: '50%',
            bgcolor: alpha(theme.palette.primary.main, 0.15),
            border: '2px solid',
            borderColor: 'primary.main',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            flexShrink: 0,
          })}
        >
          <Forum sx={{ fontSize: 24, color: 'primary.main' }} />
        </Box>

        {/* Chat input fills the remaining space */}
        <Box sx={{ flex: 1, minWidth: 0 }}>
          <ChatInput
            onSendMessage={onSendMessage}
            onCancelExecution={onCancelExecution}
            disabled={inputDisabled}
            sendingMessage={inputDisabled}
            canCancel={canCancel}
            canceling={canceling}
            compact
            placeholder={
              chatExists
                ? 'Continue the conversation — type your follow-up question here...'
                : 'Have follow-up questions? Type here to ask about this analysis...'
            }
          />
        </Box>
      </Box>
    </Paper>
  );
});

export default ChatPanel;
