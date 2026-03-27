/**
 * ChatInput — multiline text input with send/cancel buttons and character counter.
 *
 * Visual layer copied from old dashboard (ChatInput.tsx).
 * Data layer: onSendMessage and onCancelExecution are void callbacks;
 * error handling lives in the parent (ChatPanel / SessionDetailPage).
 */

import { useState } from 'react';
import {
  Box,
  TextField,
  IconButton,
  CircularProgress,
  Tooltip,
  Typography,
} from '@mui/material';
import { Send, Stop, Warning } from '@mui/icons-material';
import { MAX_MESSAGE_LENGTH, WARNING_THRESHOLD } from '../../constants/chat.ts';

interface ChatInputProps {
  onSendMessage: (content: string) => void;
  onCancelExecution?: () => void;
  disabled?: boolean;
  sendingMessage?: boolean;
  canCancel?: boolean;
  canceling?: boolean;
  /** Single-row input with tighter padding */
  compact?: boolean;
  /** Override the default placeholder text */
  placeholder?: string;
}

export default function ChatInput({
  onSendMessage,
  onCancelExecution,
  disabled,
  sendingMessage = false,
  canCancel = false,
  canceling = false,
  compact = false,
  placeholder: placeholderProp,
}: ChatInputProps) {
  const [content, setContent] = useState('');

  const isDisabled = disabled || sendingMessage;
  const isOverLimit = content.length > MAX_MESSAGE_LENGTH;
  const isNearLimit = content.length >= WARNING_THRESHOLD && content.length <= MAX_MESSAGE_LENGTH;
  const canSend = !!content.trim() && !isDisabled && !isOverLimit;

  const handleSend = () => {
    if (!canSend) return;
    onSendMessage(content.trim());
    setContent('');
  };

  const handleCancel = (event: React.MouseEvent) => {
    event.preventDefault();
    event.stopPropagation();
    if (onCancelExecution) {
      onCancelExecution();
    }
  };

  return (
    <Box>
      <Box
        sx={{
          p: compact ? 0 : { xs: 1, sm: 2 },
          pb: compact ? 1.5 : undefined,
          borderTop: compact ? 0 : 1,
          borderColor: 'divider',
          display: 'flex',
          gap: 1,
        }}
      >
        <TextField
          fullWidth
          multiline
          minRows={compact ? 1 : 2}
          maxRows={compact ? 4 : 8}
          placeholder={
            sendingMessage
              ? 'AI is processing...'
              : placeholderProp || 'Type your question... (press Enter for new line)'
          }
          value={content}
          onChange={(e) => setContent(e.target.value)}
          disabled={isDisabled}
          size="small"
          error={isOverLimit}
          helperText={
            isOverLimit
              ? `Message exceeds ${MAX_MESSAGE_LENGTH.toLocaleString()} character limit`
              : undefined
          }
          sx={{
            '& .MuiOutlinedInput-input::placeholder': {
              color: 'text.secondary',
              opacity: 0.8,
            },
            '& .MuiOutlinedInput-root': {
              fontSize: { xs: '0.875rem', sm: '1rem' },
              transition: 'all 0.3s ease',
              ...(sendingMessage && {
                opacity: 0.6,
                backgroundColor: 'action.hover',
                pointerEvents: 'none',
              }),
              ...(isOverLimit && {
                borderColor: 'error.main',
              }),
            },
          }}
        />

        {/* Show Stop button when processing and can cancel, otherwise Send button */}
        {canCancel && (sendingMessage || canceling) ? (
          <Tooltip title={canceling ? 'Stopping...' : 'Stop processing'}>
            <span>
              <IconButton
                onClick={handleCancel}
                disabled={canceling}
                sx={{
                  color: 'error.main',
                  border: '1px solid',
                  borderColor: 'error.main',
                  backgroundColor: 'transparent',
                  transition: 'all 0.2s',
                  '&:hover': {
                    backgroundColor: 'error.main',
                    borderColor: 'error.main',
                    color: 'error.contrastText',
                    transform: 'scale(1.05)',
                  },
                  '&:disabled': {
                    borderColor: 'action.disabled',
                    color: 'action.disabled',
                    opacity: 0.5,
                  },
                }}
              >
                {canceling ? <CircularProgress size={24} color="inherit" /> : <Stop />}
              </IconButton>
            </span>
          </Tooltip>
        ) : (
          <Tooltip title={sendingMessage ? 'Sending...' : 'Send message'}>
            <span>
              <IconButton
                color="primary"
                onClick={handleSend}
                disabled={!canSend}
                sx={{
                  transition: 'all 0.2s',
                  '&:hover': {
                    transform: 'scale(1.1)',
                  },
                }}
              >
                {sendingMessage ? <CircularProgress size={24} /> : <Send />}
              </IconButton>
            </span>
          </Tooltip>
        )}
      </Box>

      {/* Character counter */}
      {content.length > 0 && (
        <Box
          sx={{
            px: { xs: 1, sm: 2 },
            pt: 0.5,
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
          }}
        >
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
            {isNearLimit && !isOverLimit && (
              <Warning sx={{ fontSize: '0.875rem', color: 'warning.main' }} />
            )}
            <Typography
              variant="caption"
              sx={{
                color: isOverLimit ? 'error.main' : isNearLimit ? 'warning.main' : 'text.secondary',
                fontSize: '0.75rem',
                fontWeight: isOverLimit || isNearLimit ? 500 : 400,
              }}
            >
              {content.length.toLocaleString()} / {MAX_MESSAGE_LENGTH.toLocaleString()} characters
              {isNearLimit && !isOverLimit && ' (approaching limit)'}
            </Typography>
          </Box>
        </Box>
      )}

      {/* Subtle status message when processing */}
      {sendingMessage && (
        <Box sx={{ px: { xs: 1, sm: 2 }, pb: 1, pt: 0.5 }}>
          <Typography variant="caption" sx={{ color: 'text.secondary', fontSize: '0.75rem' }}>
            Processing your question...
          </Typography>
        </Box>
      )}
    </Box>
  );
}
