import { Box, Chip, Typography } from '@mui/material';
import { SwapHoriz } from '@mui/icons-material';
import type { ExecutionOverview } from '../../types/session';

interface ProviderFallbackIndicatorProps {
  overview: ExecutionOverview;
}

/**
 * Renders provider/backend info with a fallback indicator when
 * original_llm_provider is set (i.e., a provider fallback occurred).
 * Replaces the plain "Backend: X / Provider: Y" display in trace metadata boxes.
 */
export default function ProviderFallbackIndicator({ overview }: ProviderFallbackIndicatorProps) {
  const hasFallback = !!overview.original_llm_provider;

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

  return (
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
        sx={{ fontWeight: 600, fontSize: '0.7rem', height: 22 }}
      />
      <Typography variant="body2" color="text.secondary">
        (was: {overview.original_llm_provider}
        {overview.original_llm_backend && overview.original_llm_backend !== overview.llm_backend
          ? ` / ${overview.original_llm_backend}`
          : ''}
        )
      </Typography>
    </Box>
  );
}
