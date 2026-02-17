/**
 * LLMInteractionDetail — full expanded view of an LLM interaction.
 *
 * Shows conversation messages (system/user/assistant/tool), thinking content,
 * model info, token usage, raw request/response metadata, and copy buttons.
 *
 * Visual pattern from old dashboard's InteractionDetails.tsx (renderLLMDetails),
 * data layer rewritten for LLMInteractionDetailResponse.
 */

import { memo } from 'react';
import {
  Box,
  Typography,
  Stack,
  Accordion,
  AccordionSummary,
  AccordionDetails,
  Divider,
} from '@mui/material';
import ExpandMoreIcon from '@mui/icons-material/ExpandMore';

import type { LLMInteractionDetailResponse, ConversationMessage } from '../../types/trace';
import CopyButton from '../shared/CopyButton';
import JsonDisplay from '../shared/JsonDisplay';
import TokenUsageDisplay from '../shared/TokenUsageDisplay';
import NativeToolsDisplay from './NativeToolsDisplay';
import { getInteractionTypeLabel, formatLLMDetailForCopy, serializeMessageContent } from './traceHelpers';

interface LLMInteractionDetailProps {
  detail: LLMInteractionDetailResponse;
}

/** Get role-specific styling for conversation messages. */
function getMessageStyle(role: string) {
  switch (role) {
    case 'system':
      return { bgcolor: 'secondary.main', color: 'secondary.contrastText', label: 'System' };
    case 'user':
      return { bgcolor: 'primary.main', color: 'primary.contrastText', label: 'User' };
    case 'assistant':
      return { bgcolor: 'success.main', color: 'success.contrastText', label: 'Assistant' };
    case 'tool':
      return { bgcolor: 'warning.main', color: 'warning.contrastText', label: 'Tool' };
    default:
      return {
        bgcolor: 'grey.500',
        color: 'common.white',
        label: role.charAt(0).toUpperCase() + role.slice(1),
      };
  }
}

/** Render a single conversation message. */
function ConversationMessageView({ message, index }: { message: ConversationMessage; index: number }) {
  const style = getMessageStyle(message.role);
  const content = serializeMessageContent(message.content);

  const maxHeight = message.role === 'system' ? 200 : message.role === 'assistant' ? 300 : 200;

  return (
    <Box key={index}>
      <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 1 }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          <Box
            sx={{
              px: 1,
              py: 0.5,
              bgcolor: style.bgcolor,
              color: style.color,
              borderRadius: 1,
              fontSize: '0.75rem',
              fontWeight: 600,
              textTransform: 'uppercase',
              letterSpacing: '0.5px',
            }}
          >
            {style.label}
          </Box>
          {message.tool_call_id && (
            <Typography variant="caption" color="text.secondary" sx={{ fontFamily: 'monospace' }}>
              {message.tool_name || message.tool_call_id}
            </Typography>
          )}
        </Box>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          <Typography variant="caption" color="text.secondary" sx={{ fontWeight: 500 }}>
            {content.length.toLocaleString()} chars
          </Typography>
          <CopyButton text={content} variant="icon" size="small" tooltip={`Copy ${style.label.toLowerCase()} message`} />
        </Box>
      </Box>
      <Typography
        variant="body2"
        sx={{
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-word',
          p: 1.5,
          bgcolor: 'grey.50',
          borderRadius: 1,
          border: 1,
          borderColor: 'divider',
          maxHeight,
          overflow: 'auto',
        }}
      >
        {content}
      </Typography>
      {/* Tool calls within assistant messages */}
      {message.tool_calls && message.tool_calls.length > 0 && (
        <Box sx={{ mt: 1, pl: 2, borderLeft: '2px solid', borderColor: 'warning.main' }}>
          {message.tool_calls.map((tc, tcIdx) => (
            <Box key={tcIdx} sx={{ mb: 1 }}>
              <Typography variant="caption" sx={{ fontWeight: 600, fontFamily: 'monospace' }}>
                Tool Call: {tc.name}
              </Typography>
              <Typography
                variant="body2"
                sx={{
                  fontFamily: 'monospace',
                  fontSize: '0.8rem',
                  whiteSpace: 'pre-wrap',
                  wordBreak: 'break-word',
                  p: 1,
                  bgcolor: 'grey.50',
                  borderRadius: 1,
                  maxHeight: 150,
                  overflow: 'auto',
                }}
              >
                {tc.arguments}
              </Typography>
            </Box>
          ))}
        </Box>
      )}
    </Box>
  );
}

function LLMInteractionDetail({ detail }: LLMInteractionDetailProps) {
  const rawCopyText = (() => {
    let text = '';
    for (const msg of detail.conversation) {
      text += `${msg.role.toUpperCase()}:\n${serializeMessageContent(msg.content)}\n\n`;
    }
    text += `MODEL: ${detail.model_name}`;
    if (detail.total_tokens != null) text += ` | TOKENS: ${detail.total_tokens}`;
    return text;
  })();

  return (
    <Box sx={{ pt: 1 }}>
      <Divider sx={{ mb: 2 }} />
      <Stack spacing={2}>
        {/* Error section */}
        {detail.error_message && (
          <Box>
            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 1 }}>
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                <Box
                  sx={{
                    px: 1,
                    py: 0.5,
                    bgcolor: 'error.main',
                    color: 'error.contrastText',
                    borderRadius: 1,
                    fontSize: '0.75rem',
                    fontWeight: 600,
                    textTransform: 'uppercase',
                    letterSpacing: '0.5px',
                  }}
                >
                  Error
                </Box>
              </Box>
              <CopyButton text={detail.error_message} variant="icon" size="small" tooltip="Copy error message" />
            </Box>
            <Typography
              variant="body2"
              sx={{
                whiteSpace: 'pre-wrap',
                wordBreak: 'break-word',
                p: 1.5,
                bgcolor: 'grey.50',
                borderRadius: 1,
                border: 1,
                borderColor: 'error.main',
                color: 'error.main',
                fontFamily: 'monospace',
                fontSize: '0.875rem',
              }}
            >
              {detail.error_message}
            </Typography>
          </Box>
        )}

        {/* Conversation messages */}
        {detail.conversation.length > 0 && (
          <Box>
            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 1 }}>
              <Typography variant="subtitle2" sx={{ fontWeight: 600 }}>
                Conversation
              </Typography>
              <CopyButton
                text={formatLLMDetailForCopy(detail)}
                variant="icon"
                size="small"
                tooltip="Copy entire conversation"
              />
            </Box>
            <Stack spacing={2}>
              {detail.conversation.map((message, index) => (
                <ConversationMessageView key={index} message={message} index={index} />
              ))}
            </Stack>
          </Box>
        )}

        {/* Thinking content */}
        {detail.thinking_content && (
          <Box>
            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 1 }}>
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                <Box
                  sx={{
                    px: 1,
                    py: 0.5,
                    bgcolor: 'info.main',
                    color: 'info.contrastText',
                    borderRadius: 1,
                    fontSize: '0.75rem',
                    fontWeight: 600,
                    textTransform: 'uppercase',
                    letterSpacing: '0.5px',
                  }}
                >
                  Thinking
                </Box>
                <Typography variant="caption" color="text.secondary">
                  Internal reasoning
                </Typography>
              </Box>
              <CopyButton text={detail.thinking_content} variant="icon" size="small" tooltip="Copy thinking content" />
            </Box>
            <Typography
              variant="body2"
              sx={{
                whiteSpace: 'pre-wrap',
                wordBreak: 'break-word',
                p: 1.5,
                bgcolor: 'rgba(33, 150, 243, 0.08)',
                borderRadius: 1,
                border: 1,
                borderColor: 'info.light',
                fontStyle: 'italic',
                color: 'text.secondary',
                maxHeight: 300,
                overflow: 'auto',
              }}
            >
              {detail.thinking_content}
            </Typography>
          </Box>
        )}

        {/* Model information */}
        <Box>
          <Typography variant="subtitle2" sx={{ fontWeight: 600, mb: 1 }}>
            Model Information
          </Typography>
          <Stack direction="row" spacing={2} flexWrap="wrap" alignItems="center">
            <Typography variant="body2" color="text.secondary">
              <strong>Model:</strong> {detail.model_name}
            </Typography>
            <Typography variant="body2" color="text.secondary">
              <strong>Type:</strong> {getInteractionTypeLabel(detail.interaction_type)}
            </Typography>
          </Stack>

          {(detail.total_tokens != null || detail.input_tokens != null || detail.output_tokens != null) && (
            <Box sx={{ mt: 1.5 }}>
              <TokenUsageDisplay
                tokenData={{
                  input_tokens: detail.input_tokens,
                  output_tokens: detail.output_tokens,
                  total_tokens: detail.total_tokens,
                }}
                variant="compact"
                size="small"
                showBreakdown
                label="Tokens"
                color="info"
              />
            </Box>
          )}
        </Box>

        {/* Native Tools (enabled config + usage) */}
        <NativeToolsDisplay detail={detail} variant="detailed" />

        {/* Response Metadata (grounding details etc.) */}
        {detail.response_metadata && Object.keys(detail.response_metadata).length > 0 && (
          <Box>
            <Accordion sx={{ boxShadow: 'none', border: 1, borderColor: 'divider' }}>
              <AccordionSummary expandIcon={<ExpandMoreIcon />}>
                <Typography variant="caption" sx={{ fontWeight: 600, textTransform: 'uppercase' }}>
                  Response Metadata
                </Typography>
              </AccordionSummary>
              <AccordionDetails>
                <JsonDisplay data={detail.response_metadata} maxHeight={400} />
              </AccordionDetails>
            </Accordion>
          </Box>
        )}

        {/* Request / Response Metadata (summary counts, not full payloads) */}
        {detail.llm_request && Object.keys(detail.llm_request).length > 0 && (
          <Box>
            <Accordion sx={{ boxShadow: 'none', border: 1, borderColor: 'divider' }}>
              <AccordionSummary expandIcon={<ExpandMoreIcon />}>
                <Typography variant="caption" sx={{ fontWeight: 600, textTransform: 'uppercase' }}>
                  Request Metadata
                </Typography>
              </AccordionSummary>
              <AccordionDetails>
                <JsonDisplay data={detail.llm_request} maxHeight={400} />
              </AccordionDetails>
            </Accordion>
          </Box>
        )}

        {detail.llm_response && Object.keys(detail.llm_response).length > 0 && (
          <Box>
            <Accordion sx={{ boxShadow: 'none', border: 1, borderColor: 'divider' }}>
              <AccordionSummary expandIcon={<ExpandMoreIcon />}>
                <Typography variant="caption" sx={{ fontWeight: 600, textTransform: 'uppercase' }}>
                  Response Summary
                </Typography>
              </AccordionSummary>
              <AccordionDetails>
                <JsonDisplay data={detail.llm_response} maxHeight={400} />
              </AccordionDetails>
            </Accordion>
          </Box>
        )}

        {/* Copy buttons */}
        <Box sx={{ mt: 2, display: 'flex', gap: 1, justifyContent: 'flex-start' }}>
          <CopyButton
            text={formatLLMDetailForCopy(detail)}
            size="small"
            label="Copy All Details"
            tooltip="Copy all interaction details in formatted, human-readable format"
          />
          <CopyButton
            text={rawCopyText}
            size="small"
            label="Copy Raw Text"
            tooltip="Copy raw interaction data (unformatted)"
            buttonVariant="text"
          />
        </Box>
      </Stack>
    </Box>
  );
}

export default memo(LLMInteractionDetail);
