/**
 * InteractionCard — unified LLM/MCP interaction card with collapsed preview
 * and expandable detail (lazy-fetches full detail on expand).
 *
 * Visual pattern from old dashboard's InteractionCard.tsx,
 * data layer rewritten for new trace types.
 */

import { useState, useCallback } from 'react';
import {
  Card,
  CardHeader,
  CardContent,
  Avatar,
  Box,
  Typography,
  Chip,
  Button,
  CircularProgress,
  Alert,
} from '@mui/material';
import { useTheme, alpha } from '@mui/material/styles';
import {
  ExpandMore,
  ExpandLess,
  Psychology,
  Build,
} from '@mui/icons-material';

import { getLLMInteraction, getMCPInteraction, handleAPIError } from '../../services/api';
import type { LLMInteractionDetailResponse, MCPInteractionDetailResponse } from '../../types/trace';
import type { UnifiedInteraction } from './traceHelpers';
import {
  getInteractionTypeLabel,
  getInteractionTypeColor,
  getCardColorKey,
  computeLLMStepDescription,
  computeMCPStepDescription,
} from './traceHelpers';
import { formatDurationMs, formatTimestamp } from '../../utils/format';
import LLMInteractionPreview from './LLMInteractionPreview';
import MCPInteractionPreview from './MCPInteractionPreview';
import LLMInteractionDetail from './LLMInteractionDetail';
import MCPInteractionDetail from './MCPInteractionDetail';

import type { LLMInteractionListItem, MCPInteractionListItem } from '../../types/trace';

interface InteractionCardProps {
  interaction: UnifiedInteraction;
  sessionId: string;
}

export default function InteractionCard({ interaction, sessionId }: InteractionCardProps) {
  const theme = useTheme();
  const [isExpanded, setIsExpanded] = useState(false);
  const [detail, setDetail] = useState<LLMInteractionDetailResponse | MCPInteractionDetailResponse | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState<string | null>(null);

  const colorKey = getCardColorKey(interaction.kind, interaction.interaction_type);

  const stepDescription =
    interaction.kind === 'llm'
      ? computeLLMStepDescription(interaction as LLMInteractionListItem)
      : computeMCPStepDescription(interaction as MCPInteractionListItem);

  const handleToggle = useCallback(async () => {
    if (!isExpanded && !detail && !detailLoading) {
      setDetailLoading(true);
      setDetailError(null);
      try {
        if (interaction.kind === 'llm') {
          const data = await getLLMInteraction(sessionId, interaction.id);
          setDetail(data);
        } else {
          const data = await getMCPInteraction(sessionId, interaction.id);
          setDetail(data);
        }
      } catch (err) {
        setDetailError(handleAPIError(err));
      } finally {
        setDetailLoading(false);
      }
    }
    setIsExpanded((prev) => !prev);
  }, [isExpanded, detail, detailLoading, interaction.kind, interaction.id, sessionId]);

  return (
    <Card
      elevation={2}
      sx={{
        bgcolor: 'background.paper',
        borderRadius: 2,
        overflow: 'hidden',
        transition: 'all 0.2s ease-in-out',
        border: `2px solid ${alpha(theme.palette[colorKey].main, 0.5)}`,
        '&:hover': {
          transform: 'translateY(-1px)',
          border: `2px solid ${theme.palette[colorKey].dark}`,
        },
      }}
    >
      <CardHeader
        avatar={
          <Avatar
            sx={{
              bgcolor: `${colorKey}.main`,
              color: 'white',
              width: 40,
              height: 40,
            }}
          >
            {interaction.kind === 'llm' ? <Psychology /> : <Build />}
          </Avatar>
        }
        title={
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
            <Typography variant="subtitle1" sx={{ fontWeight: 600 }}>
              {stepDescription}
            </Typography>

            {/* Interaction type chip */}
            <Chip
              label={getInteractionTypeLabel(interaction.interaction_type)}
              size="small"
              color={getInteractionTypeColor(interaction.interaction_type)}
              sx={{ fontSize: '0.7rem', height: 22, fontWeight: 600 }}
            />

            {/* Duration chip */}
            {interaction.duration_ms != null && (
              <Chip
                label={formatDurationMs(interaction.duration_ms)}
                size="small"
                variant="filled"
                color={colorKey}
                sx={{ fontSize: '0.75rem', height: 24 }}
              />
            )}
          </Box>
        }
        subheader={
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mt: 0.5 }}>
            <Typography variant="body2" color="text.secondary">
              {formatTimestamp(interaction.created_at, 'short')}
            </Typography>
            <Typography
              variant="body2"
              sx={{ color: `${colorKey}.main`, fontWeight: 500 }}
            >
              &bull; {interaction.kind.toUpperCase()}
            </Typography>
          </Box>
        }
        action={null}
        sx={{
          pb: !isExpanded ? 2 : 1,
          bgcolor: interaction.kind === 'llm'
            ? alpha(theme.palette[colorKey].main, 0.04)
            : alpha(theme.palette.secondary.main, 0.04),
        }}
      />

      <CardContent sx={{ pt: 2, bgcolor: 'background.paper' }}>
        {/* Preview (collapsed) */}
        {!isExpanded && (
          <>
            {interaction.kind === 'llm' ? (
              <LLMInteractionPreview interaction={interaction as LLMInteractionListItem} />
            ) : (
              <MCPInteractionPreview interaction={interaction as MCPInteractionListItem} />
            )}
          </>
        )}

        {/* Expand/Collapse button */}
        <Box sx={{ display: 'flex', justifyContent: 'center', mt: 2, mb: 1 }}>
          <Button
            onClick={handleToggle}
            aria-expanded={isExpanded}
            aria-label={isExpanded ? 'Hide full details' : 'Show full details'}
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: 0.5,
              textTransform: 'none',
              py: 0.75,
              px: 1.5,
              borderRadius: 1,
              bgcolor: alpha(theme.palette[colorKey].main, 0.04),
              border: `1px solid ${alpha(theme.palette[colorKey].main, 0.12)}`,
              '&:hover': {
                bgcolor: alpha(theme.palette[colorKey].main, 0.08),
                border: `1px solid ${alpha(theme.palette[colorKey].main, 0.2)}`,
                '& .expand-text': { textDecoration: 'underline' },
              },
              transition: 'all 0.2s ease-in-out',
            }}
          >
            {detailLoading ? (
              <CircularProgress size={16} color="inherit" />
            ) : (
              <>
                <Typography
                  className="expand-text"
                  variant="body2"
                  sx={{
                    color: theme.palette[colorKey].main,
                    fontWeight: 500,
                    fontSize: '0.875rem',
                  }}
                >
                  {isExpanded ? 'Show Less' : 'Show Full Details'}
                </Typography>
                <Box
                  sx={{
                    color: theme.palette[colorKey].main,
                    display: 'flex',
                    alignItems: 'center',
                  }}
                >
                  {isExpanded ? <ExpandLess /> : <ExpandMore />}
                </Box>
              </>
            )}
          </Button>
        </Box>

        {/* Expanded detail */}
        {isExpanded && (
          <>
            {detailError && (
              <Alert severity="error" sx={{ mb: 2 }}>
                <Typography variant="body2">Failed to load details: {detailError}</Typography>
              </Alert>
            )}
            {detail && interaction.kind === 'llm' && (
              <LLMInteractionDetail detail={detail as LLMInteractionDetailResponse} />
            )}
            {detail && interaction.kind === 'mcp' && (
              <MCPInteractionDetail detail={detail as MCPInteractionDetailResponse} />
            )}
          </>
        )}
      </CardContent>
    </Card>
  );
}
