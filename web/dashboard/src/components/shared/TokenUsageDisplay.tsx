import { memo } from 'react';
import { Box, Typography, Chip, Stack } from '@mui/material';
import type { ChipProps } from '@mui/material/Chip';
import { formatTokens, formatTokensCompact } from '../../utils/format';

// Token usage data interface
export interface TokenUsageData {
  input_tokens?: number | null;
  output_tokens?: number | null;
  total_tokens?: number | null;
  cache_read_tokens?: number | null;
  cache_creation_tokens?: number | null;
}

export interface TokenUsageDisplayProps {
  tokenData: TokenUsageData;
  variant?: 'compact' | 'detailed' | 'inline' | 'labeled' | 'badge';
  size?: 'small' | 'medium' | 'large';
  showBreakdown?: boolean;
  label?: string;
  color?: ChipProps['color'];
}

function formatCacheCompactLabel(
  cacheReadTokens: number | null,
  cacheCreationTokens: number | null,
): string {
  const parts: string[] = [];
  if (cacheReadTokens != null) {
    parts.push(`${formatTokensCompact(cacheReadTokens)} cache read`);
  }
  if (cacheCreationTokens != null) {
    parts.push(`${formatTokensCompact(cacheCreationTokens)} cache create`);
  }
  return parts.join(' · ');
}

function CacheTokenSegments({
  cacheReadTokens,
  cacheCreationTokens,
  size,
  separated,
}: {
  cacheReadTokens: number | null;
  cacheCreationTokens: number | null;
  size: 'small' | 'medium' | 'large';
  separated?: boolean;
}) {
  const fs = size === 'small' ? '0.7rem' : '0.75rem';
  const labelFs = size === 'small' ? '0.65rem' : '0.7rem';
  if (cacheReadTokens == null && cacheCreationTokens == null) {
    return null;
  }
  return (
    <Box
      component="span"
      aria-label="Cache tokens"
      sx={{
        display: 'inline-flex',
        alignItems: 'baseline',
        gap: 0.5,
        flexWrap: 'wrap',
      }}
    >
      {separated && (
        <Typography variant="caption" color="text.disabled" sx={{ fontSize: labelFs, mx: 0.25 }}>
          |
        </Typography>
      )}
      {cacheReadTokens != null && (
        <Box component="span" sx={{ display: 'inline-flex', alignItems: 'baseline', gap: 0.25 }}>
          <Typography variant="caption" color="text.secondary" sx={{ fontSize: fs, fontWeight: 600 }}>
            {formatTokensCompact(cacheReadTokens)}
          </Typography>
          <Typography variant="caption" color="text.disabled" sx={{ fontSize: labelFs }}>
            cache read
          </Typography>
        </Box>
      )}
      {cacheReadTokens != null && cacheCreationTokens != null && (
        <Typography variant="caption" color="text.disabled" sx={{ fontSize: labelFs }}>
          ·
        </Typography>
      )}
      {cacheCreationTokens != null && (
        <Box component="span" sx={{ display: 'inline-flex', alignItems: 'baseline', gap: 0.25 }}>
          <Typography variant="caption" color="text.secondary" sx={{ fontSize: fs, fontWeight: 600 }}>
            {formatTokensCompact(cacheCreationTokens)}
          </Typography>
          <Typography variant="caption" color="text.disabled" sx={{ fontSize: labelFs }}>
            cache create
          </Typography>
        </Box>
      )}
    </Box>
  );
}

/**
 * TokenUsageDisplay component
 * Reusable component for displaying token usage at any aggregation level
 */
function TokenUsageDisplay({
  tokenData,
  variant = 'detailed',
  size = 'medium',
  showBreakdown = true,
  label,
  color = 'default'
}: TokenUsageDisplayProps) {
  
  const totalTokens = tokenData.total_tokens ?? null;
  const inputTokens = tokenData.input_tokens ?? null;
  const outputTokens = tokenData.output_tokens ?? null;
  const cacheReadTokens = tokenData.cache_read_tokens ?? null;
  const cacheCreationTokens = tokenData.cache_creation_tokens ?? null;
  const hasCache = cacheReadTokens != null || cacheCreationTokens != null;
  const hasMain = inputTokens != null || outputTokens != null || totalTokens != null;

  if ([totalTokens, inputTokens, outputTokens, cacheReadTokens, cacheCreationTokens].every(v => v == null)) {
    return null;
  }

  const getTokenColor = (tokens: number | null): ChipProps['color'] => {
    if (tokens == null) return 'default';
    if (tokens > 5000) return 'error';
    if (tokens > 2000) return 'warning';
    if (tokens > 1000) return 'info';
    return 'success';
  };

  const cacheSegments = (
    <CacheTokenSegments
      cacheReadTokens={cacheReadTokens}
      cacheCreationTokens={cacheCreationTokens}
      size={size}
      separated={hasMain}
    />
  );

  // Badge variant - simple chip display
  if (variant === 'badge') {
    const hasInputOutput = inputTokens != null || outputTokens != null;
    const cacheLabel = formatCacheCompactLabel(cacheReadTokens, cacheCreationTokens);
    const baseLabel = hasInputOutput
      ? `${formatTokensCompact(inputTokens)} • ${formatTokensCompact(outputTokens)} = ${formatTokensCompact(totalTokens)}`
      : totalTokens != null
        ? formatTokensCompact(totalTokens)
        : '';
    const chipLabel =
      baseLabel && cacheLabel ? `${baseLabel} (${cacheLabel})` : [baseLabel, cacheLabel].filter(Boolean).join(' ');
    return (
      <Chip
        size={size === 'large' ? 'medium' : size}
        label={chipLabel}
        color={color === 'default' ? getTokenColor(totalTokens) : color}
        variant="outlined"
        sx={{ 
          fontSize: size === 'small' ? '0.75rem' : undefined,
          fontWeight: 600 
        }}
      />
    );
  }

  // Inline variant - minimal text display
  if (variant === 'inline') {
    return (
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.25, flexWrap: 'wrap' }}>
        {label && (
          <Typography 
            variant="caption" 
            color="text.secondary"
            sx={{ 
              fontSize: size === 'small' ? '0.7rem' : '0.75rem',
              fontWeight: 500 
            }}
          >
            {label}:
          </Typography>
        )}
        {(inputTokens != null || outputTokens != null) ? (
          <>
            <Typography 
              variant="caption"
              sx={{ 
                fontSize: size === 'small' ? '0.7rem' : '0.75rem',
                fontWeight: 600,
                color: 'info.main'
              }}
            >
              {formatTokensCompact(inputTokens)}
            </Typography>
            <Typography 
              variant="caption" 
              color="text.disabled"
              sx={{ fontSize: size === 'small' ? '0.65rem' : '0.7rem' }}
            >
              •
            </Typography>
            <Typography 
              variant="caption"
              sx={{ 
                fontSize: size === 'small' ? '0.7rem' : '0.75rem',
                fontWeight: 600,
                color: 'success.main'
              }}
            >
              {formatTokensCompact(outputTokens)}
            </Typography>
            <Typography 
              variant="caption" 
              color="text.disabled"
              sx={{ fontSize: size === 'small' ? '0.65rem' : '0.7rem' }}
            >
              =
            </Typography>
            <Typography 
              variant="caption"
              sx={{ 
                fontSize: size === 'small' ? '0.7rem' : '0.75rem',
                fontWeight: 700,
                color: totalTokens && totalTokens > 5000 ? 'error.main' : 
                       totalTokens && totalTokens > 2000 ? 'warning.main' : 'text.primary'
              }}
            >
              {formatTokensCompact(totalTokens)}
            </Typography>
          </>
        ) : totalTokens !== null ? (
          <Typography
            variant="caption"
            sx={{
              fontSize: size === 'small' ? '0.7rem' : '0.75rem',
              fontWeight: 700,
              color: totalTokens > 5000 ? 'error.main' : totalTokens > 2000 ? 'warning.main' : 'text.primary',
            }}
          >
            {formatTokensCompact(totalTokens)}
          </Typography>
        ) : !hasCache ? (
          <Typography variant="caption" color="text.secondary" sx={{ fontSize: size === 'small' ? '0.7rem' : '0.75rem', fontWeight: 500 }}>
            —
          </Typography>
        ) : null}
        {cacheSegments}
      </Box>
    );
  }

  // Labeled variant - "59K total 57K in 947 out" with colored numbers and text labels
  if (variant === 'labeled') {
    const fs = size === 'small' ? '0.7rem' : '0.75rem';
    const labelFs = size === 'small' ? '0.65rem' : '0.7rem';
    return (
      <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 0.5, flexWrap: 'wrap' }}>
        {totalTokens != null && (
          <>
            <Typography variant="caption" sx={{ fontSize: fs, fontWeight: 700, color: 'warning.main' }}>
              {formatTokensCompact(totalTokens)}
            </Typography>
            <Typography variant="caption" color="text.disabled" sx={{ fontSize: labelFs }}>total</Typography>
          </>
        )}
        {inputTokens != null && (
          <>
            <Typography variant="caption" sx={{ fontSize: fs, fontWeight: 600, color: 'info.main' }}>
              {formatTokensCompact(inputTokens)}
            </Typography>
            <Typography variant="caption" color="text.disabled" sx={{ fontSize: labelFs }}>in</Typography>
          </>
        )}
        {outputTokens != null && (
          <>
            <Typography variant="caption" sx={{ fontSize: fs, fontWeight: 600, color: 'success.main' }}>
              {formatTokensCompact(outputTokens)}
            </Typography>
            <Typography variant="caption" color="text.disabled" sx={{ fontSize: labelFs }}>out</Typography>
          </>
        )}
        {cacheSegments}
      </Box>
    );
  }

  // Compact variant - single line with full breakdown
  if (variant === 'compact') {
    return (
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5, flexWrap: 'wrap' }}>
        {label && (
          <Typography 
            variant="caption" 
            sx={{ 
              fontWeight: 600,
              fontSize: size === 'small' ? '0.7rem' : '0.75rem',
              color: 'text.secondary' 
            }}
          >
            {label}:
          </Typography>
        )}
        {(inputTokens != null || outputTokens != null) ? (
          <>
            <Typography 
              variant="caption"
              sx={{ 
                fontSize: size === 'small' ? '0.7rem' : '0.75rem',
                fontWeight: 600,
                color: 'info.main'
              }}
            >
              {formatTokensCompact(inputTokens)}
            </Typography>
            <Typography 
              variant="caption" 
              color="text.disabled"
              sx={{ fontSize: size === 'small' ? '0.65rem' : '0.7rem' }}
            >
              •
            </Typography>
            <Typography 
              variant="caption"
              sx={{ 
                fontSize: size === 'small' ? '0.7rem' : '0.75rem',
                fontWeight: 600,
                color: 'success.main'
              }}
            >
              {formatTokensCompact(outputTokens)}
            </Typography>
            <Typography 
              variant="caption" 
              color="text.disabled"
              sx={{ fontSize: size === 'small' ? '0.65rem' : '0.7rem' }}
            >
              =
            </Typography>
            <Typography 
              variant="caption"
              sx={{ 
                fontSize: size === 'small' ? '0.7rem' : '0.75rem',
                fontWeight: 700,
                color: totalTokens && totalTokens > 5000 ? 'error.main' : 
                       totalTokens && totalTokens > 2000 ? 'warning.main' : 'text.primary'
              }}
            >
              {formatTokensCompact(totalTokens)}
            </Typography>
          </>
        ) : totalTokens != null ? (
          <Typography
            variant="caption"
            sx={{
              fontSize: size === 'small' ? '0.7rem' : '0.75rem',
              fontWeight: 700,
              color: totalTokens > 5000 ? 'error.main' : 
                     totalTokens > 2000 ? 'warning.main' : 'text.primary'
            }}
          >
            {formatTokensCompact(totalTokens)}
          </Typography>
        ) : !hasCache ? (
          <Typography 
            variant="caption" 
            color="text.secondary" 
            sx={{ 
              fontSize: size === 'small' ? '0.7rem' : '0.75rem', 
              fontWeight: 500 
            }}
          >
            —
          </Typography>
        ) : null}
        {cacheSegments}
      </Box>
    );
  }

  // Detailed variant - full breakdown with styling
  return (
    <Box>
      {label && (
        <Typography 
          variant="subtitle2" 
          sx={{ 
            fontWeight: 600, 
            mb: 1,
            fontSize: size === 'small' ? '0.8rem' : undefined,
            color: 'text.secondary'
          }}
        >
          {label}
        </Typography>
      )}
      
      <Stack 
        direction={size === 'small' ? 'column' : 'row'} 
        spacing={size === 'small' ? 0.5 : 2} 
        flexWrap="wrap"
        alignItems={size === 'small' ? 'flex-start' : 'center'}
      >
        {(totalTokens != null || inputTokens != null || outputTokens != null) && (
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
            <Typography 
              variant="body2" 
              color="text.secondary"
              sx={{ 
                fontSize: size === 'small' ? '0.75rem' : undefined,
                fontWeight: 500 
              }}
            >
              <strong>Total:</strong>
            </Typography>
            <Typography 
              variant="body2"
              sx={{ 
                fontWeight: 600,
                fontSize: size === 'small' ? '0.8rem' : '0.875rem',
                color: totalTokens && totalTokens > 2000 ? 'warning.main' : 'text.primary'
              }}
            >
              {formatTokens(totalTokens)}
            </Typography>
          </Box>
        )}

        {showBreakdown && (inputTokens != null || outputTokens != null) && (
          <>
            {inputTokens !== null && (
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
                <Typography 
                  variant="body2" 
                  color="text.secondary"
                  sx={{ fontSize: size === 'small' ? '0.75rem' : undefined }}
                >
                  <strong>Input:</strong>
                </Typography>
                <Typography 
                  variant="body2" 
                  color="info.main"
                  sx={{ 
                    fontSize: size === 'small' ? '0.8rem' : undefined,
                    fontWeight: 500 
                  }}
                >
                  {formatTokens(inputTokens)}
                </Typography>
              </Box>
            )}

            {outputTokens !== null && (
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
                <Typography 
                  variant="body2" 
                  color="text.secondary"
                  sx={{ fontSize: size === 'small' ? '0.75rem' : undefined }}
                >
                  <strong>Output:</strong>
                </Typography>
                <Typography 
                  variant="body2" 
                  color="success.main"
                  sx={{ 
                    fontSize: size === 'small' ? '0.8rem' : undefined,
                    fontWeight: 500 
                  }}
                >
                  {formatTokens(outputTokens)}
                </Typography>
              </Box>
            )}
          </>
        )}

        {cacheReadTokens != null && (
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
            <Typography
              variant="body2"
              color="text.secondary"
              sx={{ fontSize: size === 'small' ? '0.75rem' : undefined }}
            >
              <strong>Cache read:</strong>
            </Typography>
            <Typography
              variant="body2"
              sx={{
                fontSize: size === 'small' ? '0.8rem' : undefined,
                fontWeight: 500,
              }}
            >
              {formatTokens(cacheReadTokens)}
            </Typography>
          </Box>
        )}

        {cacheCreationTokens != null && (
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
            <Typography
              variant="body2"
              color="text.secondary"
              sx={{ fontSize: size === 'small' ? '0.75rem' : undefined }}
            >
              <strong>Cache create:</strong>
            </Typography>
            <Typography
              variant="body2"
              sx={{
                fontSize: size === 'small' ? '0.8rem' : undefined,
                fontWeight: 500,
              }}
            >
              {formatTokens(cacheCreationTokens)}
            </Typography>
          </Box>
        )}
      </Stack>
    </Box>
  );
}

export default memo(TokenUsageDisplay);
