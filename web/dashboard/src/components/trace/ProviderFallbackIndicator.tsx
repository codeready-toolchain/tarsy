import { useState } from 'react';
import { Box, Chip, Collapse, IconButton, Typography, alpha } from '@mui/material';
import { SwapHoriz, ExpandMore, ExpandLess } from '@mui/icons-material';
import type { ExecutionOverview } from '../../types/session';

interface ProviderFallbackIndicatorProps {
  overview: ExecutionOverview;
}

function formatError(raw: string): string {
  return raw
    .replace(/\\n/g, '\n')
    .replace(/\)\s*\(/g, ')\n(')
    .trim();
}

function stripEnvelope(raw: string): string {
  let msg = raw.replace(/^LLM error:\s*/i, '');
  msg = msg.replace(/\s*\(code:\s*\w+,?\s*retryable:\s*\w+\)\s*$/i, '');
  msg = msg.replace(/\s*\(attempt\s*\d+\)\s*$/i, '');
  return msg.trim();
}

/**
 * Renders provider/backend info with a fallback indicator when
 * original_llm_provider is set (i.e., a provider fallback occurred).
 * Replaces the plain "Backend: X / Provider: Y" display in trace metadata boxes.
 */
export default function ProviderFallbackIndicator({ overview }: ProviderFallbackIndicatorProps) {
  const hasFallback = !!overview.original_llm_provider;
  const [expanded, setExpanded] = useState(false);

  if (!hasFallback) {
    return (
      <>
        {overview.llm_backend && (
          <Typography variant="body2" color="text.secondary">
            <strong>Backend:</strong> {overview.llm_backend}
          </Typography>
        )}
        {overview.llm_provider && (
          <Typography variant="body2" color="text.secondary">
            <strong>Provider:</strong> {overview.llm_provider}
          </Typography>
        )}
      </>
    );
  }

  const reason = overview.fallback_reason || '';
  const errorCode = overview.fallback_error_code || '';
  const attempt = overview.fallback_attempt;
  const hasDetails = reason.length > 0;
  const formattedReason = reason ? formatError(stripEnvelope(reason)) : '';

  return (
    <Box sx={{ width: '100%' }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5, flexWrap: 'wrap' }}>
        {overview.llm_backend && (
          <Typography variant="body2" color="text.secondary">
            <strong>Backend:</strong> {overview.llm_backend}
          </Typography>
        )}
        {overview.llm_provider && (
          <Typography variant="body2" color="text.secondary">
            <strong>Provider:</strong> {overview.llm_provider}
          </Typography>
        )}
        <Chip
          icon={<SwapHoriz sx={{ fontSize: '0.9rem' }} />}
          label="Fallback"
          size="small"
          color="warning"
          variant="outlined"
          sx={{ fontWeight: 600, fontSize: '0.7rem', height: 22, cursor: hasDetails ? 'pointer' : 'default' }}
          onClick={hasDetails ? () => setExpanded((prev) => !prev) : undefined}
        />
        <Typography variant="body2" color="text.secondary">
          (was: {overview.original_llm_provider}
          {overview.original_llm_backend && overview.original_llm_backend !== overview.llm_backend
            ? ` / ${overview.original_llm_backend}`
            : ''}
          )
        </Typography>
        {errorCode && (
          <Chip label={errorCode} size="small" variant="outlined" color="warning" sx={{ height: 20, fontSize: '0.65rem' }} />
        )}
        {attempt != null && (
          <Typography variant="caption" color="text.secondary" sx={{ fontSize: '0.7rem' }}>
            attempt {attempt}
          </Typography>
        )}
        {hasDetails && (
          <IconButton size="small" sx={{ p: 0.25 }} onClick={() => setExpanded((prev) => !prev)}>
            {expanded ? <ExpandLess fontSize="small" /> : <ExpandMore fontSize="small" />}
          </IconButton>
        )}
      </Box>

      <Collapse in={expanded}>
        <Box
          sx={(theme) => ({
            mt: 1,
            p: 1.5,
            bgcolor: alpha(theme.palette.warning.main, 0.04),
            border: `1px solid ${alpha(theme.palette.warning.main, 0.2)}`,
            borderRadius: 1,
          })}
        >
          <Typography variant="body2" color="text.secondary" sx={{ mb: 0.75, fontStyle: 'italic' }}>
            The original model ({overview.original_llm_provider}) returned an error, so execution was switched to {overview.llm_provider}.
          </Typography>
          {formattedReason && (
            <Box>
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
                  maxHeight: 200,
                  overflow: 'auto',
                })}
              >
                {formattedReason}
              </Box>
            </Box>
          )}
        </Box>
      </Collapse>
    </Box>
  );
}
