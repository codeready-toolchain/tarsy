import { memo } from 'react';
import { Box, Typography, Chip, Stack } from '@mui/material';
import type { ChipProps } from '@mui/material/Chip';

// Token usage data interface
export interface TokenUsageData {
  input_tokens?: number | null;
  output_tokens?: number | null;
  total_tokens?: number | null;
}

export interface TokenUsageDisplayProps {
  tokenData: TokenUsageData;
  variant?: 'compact' | 'detailed' | 'inline' | 'badge';
  size?: 'small' | 'medium' | 'large';
  showBreakdown?: boolean;
  label?: string;
  color?: ChipProps['color'];
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

  if ([totalTokens, inputTokens, outputTokens].every(v => v == null)) {
    return null;
  }

  const formatTokens = (tokens: number | null): string => {
    if (tokens === null || tokens === undefined) return '—';
    return tokens.toLocaleString();
  };

  const formatTokensCompact = (tokens: number | null): string => {
    if (tokens === null || tokens === undefined) return '—';
    if (tokens >= 1000) {
      return (tokens / 1000).toFixed(1) + 'K';
    }
    return tokens.toString();
  };

  const getTokenColor = (tokens: number | null): ChipProps['color'] => {
    if (tokens == null) return 'default';
    if (tokens > 5000) return 'error';
    if (tokens > 2000) return 'warning';
    if (tokens > 1000) return 'info';
    return 'success';
  };

  // Badge variant - simple chip display
  if (variant === 'badge') {
    const hasInputOutput = inputTokens != null || outputTokens != null;
    return (
      <Chip
        size={size === 'large' ? 'medium' : size}
        label={
          hasInputOutput
            ? `${formatTokensCompact(inputTokens)} • ${formatTokensCompact(outputTokens)} = ${formatTokensCompact(totalTokens)}`
            : formatTokensCompact(totalTokens)
        }
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
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.25 }}>
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
        ) : (
          <Typography variant="caption" color="text.secondary" sx={{ fontSize: size === 'small' ? '0.7rem' : '0.75rem', fontWeight: 500 }}>
            —
          </Typography>
        )}
      </Box>
    );
  }

  // Compact variant - single line with full breakdown
  if (variant === 'compact') {
    return (
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
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
        ) : (
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
        )}
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
      </Stack>
    </Box>
  );
}

export default memo(TokenUsageDisplay);
