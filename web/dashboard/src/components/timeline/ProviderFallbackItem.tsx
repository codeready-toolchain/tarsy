import { memo, useState } from 'react';
import { Box, Chip, Collapse, IconButton, Typography, alpha } from '@mui/material';
import { SwapHoriz, ExpandMore, ExpandLess } from '@mui/icons-material';
import { highlightSearchTermNodes } from '../../utils/search';
import type { FlowItem } from '../../utils/timelineParser';

interface ProviderFallbackItemProps {
  item: FlowItem;
  searchTerm?: string;
}

function safeString(value: unknown): string {
  return typeof value === 'string' ? value : String(value ?? '');
}

function extractErrorCode(meta: Record<string, unknown>): string {
  if (typeof meta.error_code === 'string' && meta.error_code) return meta.error_code;
  const reason = safeString(meta.reason);
  if (!reason) return '';
  const match = reason.match(/code:\s*(\w+)/);
  return match ? match[1] : '';
}

/**
 * Turns literal "\n" (two-char sequences) into real newlines, and
 * pretty-prints any embedded JSON-like blocks.
 */
function formatErrorMessage(raw: string): string {
  return raw
    .replace(/\\n/g, '\n')
    .replace(/\)\s*\(/g, ')\n(')
    .trim();
}

/**
 * Strips the wrapping "LLM error: ... (code: X, retryable: Y)" envelope
 * that streaming.go adds, leaving just the provider message.
 */
function stripEnvelope(raw: string): string {
  let msg = raw.replace(/^LLM error:\s*/i, '');
  msg = msg.replace(/\s*\(code:\s*\w+,?\s*retryable:\s*\w+\)\s*$/i, '');
  msg = msg.replace(/\s*\(attempt\s*\d+\)\s*$/i, '');
  return msg.trim();
}

function ProviderFallbackItem({ item, searchTerm }: ProviderFallbackItemProps) {
  const meta = item.metadata || {};
  const from = safeString(meta.original_provider) || '?';
  const to = safeString(meta.fallback_provider) || '?';
  const fromBackend = safeString(meta.original_backend);
  const toBackend = safeString(meta.fallback_backend);
  const reason = safeString(meta.reason);
  const attempt = typeof meta.attempt === 'number' ? meta.attempt : undefined;
  const droppedTools = Array.isArray(meta.native_tools_dropped) ? meta.native_tools_dropped as string[] : undefined;
  const errorCode = extractErrorCode(meta);
  const errorRetryable = typeof meta.error_retryable === 'boolean' ? meta.error_retryable : undefined;

  const [expanded, setExpanded] = useState(false);
  const hasDetails = reason.length > 0 || (droppedTools && droppedTools.length > 0);

  const strippedMessage = stripEnvelope(reason);
  const formattedMessage = formatErrorMessage(strippedMessage);

  return (
    <Box data-flow-item-id={item.id} sx={{ my: 2 }}>
      <Box sx={{ display: 'flex', alignItems: 'stretch' }}>
        <Box
          sx={(theme) => ({
            width: 4,
            bgcolor: theme.palette.warning.main,
            borderRadius: 1,
            flexShrink: 0,
          })}
        />
        <Box sx={{ flex: 1, minWidth: 0 }}>
          <Box
            onClick={() => hasDetails && setExpanded((prev) => !prev)}
            sx={(theme) => ({
              display: 'flex',
              alignItems: 'center',
              gap: 1,
              flexWrap: 'wrap',
              px: 1.5,
              py: 0.75,
              bgcolor: alpha(theme.palette.warning.main, 0.06),
              borderTopRightRadius: 4,
              borderBottomRightRadius: expanded ? 0 : 4,
              cursor: hasDetails ? 'pointer' : 'default',
              transition: 'background-color 0.2s ease',
              '&:hover': hasDetails ? { bgcolor: alpha(theme.palette.warning.main, 0.12) } : {},
            })}
          >
            <SwapHoriz sx={{ fontSize: 18, color: 'warning.main' }} />
            <Typography variant="caption" sx={{ fontWeight: 800, color: 'warning.main', fontSize: '0.75rem', letterSpacing: 0.3, textTransform: 'uppercase' }}>
              Provider Fallback
            </Typography>
            <Typography variant="caption" sx={{ fontFamily: 'monospace', fontSize: '0.75rem', color: 'text.primary', fontWeight: 600 }}>
              {from} &rarr; {to}
            </Typography>
            {errorCode && (
              <Chip label={errorCode} size="small" variant="outlined" color="warning" sx={{ height: 20, fontSize: '0.65rem' }} />
            )}
            {attempt != null && (
              <Typography variant="caption" color="text.secondary" sx={{ fontSize: '0.7rem' }}>
                attempt {attempt}
              </Typography>
            )}
            {droppedTools && droppedTools.length > 0 && !expanded && (
              <Chip
                label={`${droppedTools.length} tools dropped`}
                size="small"
                variant="outlined"
                color="warning"
                sx={{ height: 18, fontSize: '0.6rem' }}
              />
            )}
            {hasDetails && (
              <IconButton size="small" sx={{ p: 0.25, ml: 'auto' }}>
                {expanded ? <ExpandLess fontSize="small" /> : <ExpandMore fontSize="small" />}
              </IconButton>
            )}
          </Box>

          <Collapse in={expanded}>
            <Box
              sx={(theme) => ({
                px: 1.5,
                py: 1,
                bgcolor: alpha(theme.palette.warning.main, 0.04),
                borderTop: '1px solid',
                borderColor: alpha(theme.palette.warning.main, 0.15),
                borderBottomRightRadius: 4,
              })}
            >
              <Typography variant="body2" color="text.secondary" sx={{ display: 'block', mb: 0.75, fontStyle: 'italic' }}>
                The original model ({from}) returned an error, so execution was automatically switched to {to}.
              </Typography>

              {fromBackend && toBackend && fromBackend !== toBackend && (
                <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mb: 0.5, fontSize: '0.7rem' }}>
                  Backend: {fromBackend} &rarr; {toBackend}
                </Typography>
              )}

              {(errorCode || errorRetryable != null) && (
                <Box sx={{ display: 'flex', gap: 1, mb: 0.5, flexWrap: 'wrap', alignItems: 'center' }}>
                  {errorCode && (
                    <Typography variant="caption" color="text.secondary" sx={{ fontSize: '0.7rem' }}>
                      <strong>Code:</strong> {errorCode}
                    </Typography>
                  )}
                  {errorRetryable != null && (
                    <Typography variant="caption" color="text.secondary" sx={{ fontSize: '0.7rem' }}>
                      <strong>Retryable:</strong> {String(errorRetryable)}
                    </Typography>
                  )}
                </Box>
              )}

              {formattedMessage && (
                <Box sx={{ mb: droppedTools?.length ? 1 : 0 }}>
                  <Typography variant="caption" sx={{ fontWeight: 600, fontSize: '0.7rem', display: 'block', mb: 0.5 }}>
                    Error:
                  </Typography>
                  <Box
                    component="pre"
                    sx={(theme) => ({
                      m: 0,
                      px: 1.5,
                      py: 1,
                      fontFamily: 'monospace',
                      fontSize: '0.72rem',
                      lineHeight: 1.6,
                      whiteSpace: 'pre-wrap',
                      wordBreak: 'break-word',
                      color: theme.palette.text.secondary,
                      bgcolor: theme.palette.grey[50],
                      border: `1px solid ${theme.palette.divider}`,
                      borderRadius: 1,
                      maxHeight: 250,
                      overflow: 'auto',
                    })}
                  >
                    {searchTerm ? highlightSearchTermNodes(formattedMessage, searchTerm) : formattedMessage}
                  </Box>
                </Box>
              )}

              {droppedTools && droppedTools.length > 0 && (
                <Typography variant="caption" color="text.secondary" sx={{ display: 'block', fontSize: '0.7rem', mt: 0.5 }}>
                  <strong>Native tools dropped:</strong> {droppedTools.join(', ')}
                </Typography>
              )}
            </Box>
          </Collapse>
        </Box>
      </Box>
    </Box>
  );
}

export default memo(ProviderFallbackItem);
