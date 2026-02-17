/**
 * LLMInteractionPreview — collapsed preview for LLM interactions.
 *
 * Shows model name, interaction type chip, and token summary.
 * Visual pattern from old dashboard's LLMInteractionPreview.tsx,
 * data layer rewritten for new TraceListResponse types.
 */

import { Box, Typography, Chip } from '@mui/material';

import type { LLMInteractionListItem } from '../../types/trace';
import TokenUsageDisplay from '../shared/TokenUsageDisplay';
import { getInteractionTypeLabel, getInteractionTypeColor } from './traceHelpers';

interface LLMInteractionPreviewProps {
  interaction: LLMInteractionListItem;
}

export default function LLMInteractionPreview({ interaction }: LLMInteractionPreviewProps) {
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
      {/* Type and model */}
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
        <Chip
          label={getInteractionTypeLabel(interaction.interaction_type)}
          size="small"
          color={getInteractionTypeColor(interaction.interaction_type)}
          sx={{ fontSize: '0.7rem', height: 22, fontWeight: 600 }}
        />
        <Typography variant="body2" color="text.secondary">
          {interaction.model_name}
        </Typography>
      </Box>

      {/* Token usage */}
      {(interaction.total_tokens != null ||
        interaction.input_tokens != null ||
        interaction.output_tokens != null) && (
        <TokenUsageDisplay
          tokenData={{
            input_tokens: interaction.input_tokens,
            output_tokens: interaction.output_tokens,
            total_tokens: interaction.total_tokens,
          }}
          variant="compact"
          size="small"
          showBreakdown
          label="Tokens"
          color="info"
        />
      )}

      {/* Error indicator */}
      {interaction.error_message && (
        <Typography
          variant="body2"
          color="error.main"
          sx={{ fontWeight: 500, fontSize: '0.8rem' }}
        >
          Error: {interaction.error_message}
        </Typography>
      )}
    </Box>
  );
}
