/**
 * ChatPanel — collapsible follow-up chat input panel.
 *
 * Visual layer copied from old dashboard (ChatPanel.tsx).
 * Simplified: no createChat API call on expand (new backend auto-creates
 * chat on first message), no Chat object prop (just chatExists boolean).
 *
 * Positioned between ConversationTimeline and FinalAnalysisCard.
 * Chat messages themselves render in the timeline, not here.
 */

import { useState, useEffect } from 'react';
import {
  Box,
  Paper,
  IconButton,
  Collapse,
  Typography,
  Alert,
  alpha,
} from '@mui/material';
import { AccountCircle, ExpandMore } from '@mui/icons-material';
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
  /** External trigger to expand (e.g. "Jump to Chat" button). */
  forceExpand?: number;
  /** Callback to collapse FinalAnalysisCard when chat is expanded. */
  onCollapseAnalysis?: () => void;
}

export default function ChatPanel({
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
  forceExpand = 0,
  onCollapseAnalysis,
}: ChatPanelProps) {
  const [expanded, setExpanded] = useState(false);

  // Handle external expansion trigger (e.g. from "Jump to Chat" button)
  useEffect(() => {
    if (forceExpand > 0 && !expanded) {
      // Collapse Final Analysis first, then expand chat
      onCollapseAnalysis?.();
      // Brief delay for Final Analysis to start collapsing
      const timer = setTimeout(() => {
        setExpanded(true);
        // Scroll to bottom after expansion
        setTimeout(() => {
          window.scrollTo({
            top: document.documentElement.scrollHeight,
            behavior: 'smooth',
          });
        }, 500);
      }, 150);
      return () => clearTimeout(timer);
    }
  }, [forceExpand]); // eslint-disable-line react-hooks/exhaustive-deps

  const handleToggle = () => {
    if (expanded) {
      setExpanded(false);
      return;
    }

    // Collapse Final Analysis when expanding chat
    onCollapseAnalysis?.();

    // Expand after brief delay for Final Analysis to start collapsing
    setTimeout(() => {
      setExpanded(true);
      // Scroll to bottom after expansion
      setTimeout(() => {
        window.scrollTo({
          top: document.documentElement.scrollHeight,
          behavior: 'smooth',
        });
      }, 500);
    }, 150);
  };

  if (!isAvailable) return null;

  const inputDisabled = sendingMessage || chatStageInProgress;

  return (
    <Paper
      elevation={expanded ? 3 : 1}
      sx={(theme) => ({
        mt: 3,
        overflow: 'hidden',
        transition: 'all 0.3s ease-in-out',
        border: `2px solid ${expanded ? theme.palette.primary.main : 'transparent'}`,
        '&:hover': {
          borderColor: !expanded
            ? alpha(theme.palette.primary.main, 0.3)
            : theme.palette.primary.main,
        },
      })}
    >
      {/* Collapsible Header — Clickable to expand/collapse */}
      <Box
        onClick={handleToggle}
        sx={(theme) => ({
          p: 2.5,
          display: 'flex',
          alignItems: 'center',
          cursor: 'pointer',
          bgcolor: expanded
            ? alpha(theme.palette.primary.main, 0.06)
            : alpha(theme.palette.primary.main, 0.03),
          transition: 'all 0.3s ease-in-out',
          borderBottom: expanded ? `1px solid ${theme.palette.divider}` : 'none',
          '&:hover': {
            bgcolor: alpha(theme.palette.primary.main, 0.08),
          },
        })}
      >
        {/* Chat Icon */}
        <Box
          sx={{
            width: 40,
            height: 40,
            borderRadius: '50%',
            bgcolor: 'primary.main',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            mr: 2,
            flexShrink: 0,
          }}
        >
          <AccountCircle sx={{ fontSize: 40, color: 'white' }} />
        </Box>

        {/* Text Content */}
        <Box sx={{ flex: 1 }}>
          <Typography
            variant="h6"
            sx={{
              fontWeight: 600,
              mb: 0.3,
              color: 'text.primary',
              fontSize: '1rem',
            }}
          >
            {chatExists ? 'Follow-up Chat' : 'Have follow-up questions?'}
          </Typography>
          <Typography
            variant="body2"
            sx={{
              color: 'text.secondary',
              fontSize: '0.85rem',
            }}
          >
            {expanded
              ? 'Ask questions about this analysis'
              : 'Click to expand and continue the conversation'}
          </Typography>
        </Box>

        {/* Expand/Collapse Icon */}
        <IconButton
          size="small"
          onClick={(e) => {
            e.stopPropagation();
            handleToggle();
          }}
          sx={{
            transition: 'transform 0.3s',
            transform: expanded ? 'rotate(180deg)' : 'rotate(0deg)',
          }}
        >
          <ExpandMore />
        </IconButton>
      </Box>

      {/* Error Display (shown when collapsed if there's an error) */}
      {!expanded && error && (
        <Alert severity="error" sx={{ m: 2 }} onClose={onClearError}>
          <Typography variant="body2">{error}</Typography>
        </Alert>
      )}

      {/* Chat Input — Only shown when expanded */}
      <Collapse in={expanded} timeout={400}>
        <Box sx={{ display: 'flex', flexDirection: 'column' }}>
          {error && (
            <Alert severity="error" sx={{ m: 2, mb: 0 }} onClose={onClearError}>
              <Typography variant="body2">{error}</Typography>
            </Alert>
          )}

          {/* Simple static indicator when processing */}
          {inputDisabled && (
            <Box
              sx={(theme) => ({
                height: 3,
                width: '100%',
                bgcolor: alpha(theme.palette.primary.main, 0.15),
              })}
            />
          )}

          <ChatInput
            onSendMessage={onSendMessage}
            onCancelExecution={onCancelExecution}
            disabled={inputDisabled}
            sendingMessage={inputDisabled}
            canCancel={canCancel}
            canceling={canceling}
          />
        </Box>
      </Collapse>
    </Paper>
  );
}
