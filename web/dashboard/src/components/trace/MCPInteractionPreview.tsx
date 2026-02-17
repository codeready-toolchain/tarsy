/**
 * MCPInteractionPreview — collapsed preview for MCP interactions.
 *
 * Shows server badge, tool name, and status indicator.
 * Visual pattern from old dashboard's MCPInteractionPreview.tsx,
 * data layer rewritten for new TraceListResponse types.
 */

import { Box, Typography, Chip } from '@mui/material';

import type { MCPInteractionListItem } from '../../types/trace';
import { MCP_INTERACTION_TYPE, MCP_LIST_TOOLS_NAME } from '../../constants/interactionTypes';
import { getInteractionTypeLabel } from './traceHelpers';

interface MCPInteractionPreviewProps {
  interaction: MCPInteractionListItem;
}

export default function MCPInteractionPreview({ interaction }: MCPInteractionPreviewProps) {
  const isToolList =
    interaction.interaction_type === MCP_INTERACTION_TYPE.TOOL_LIST ||
    (interaction.interaction_type === MCP_INTERACTION_TYPE.TOOL_CALL && interaction.tool_name === MCP_LIST_TOOLS_NAME);

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
      {/* Server and tool */}
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
        <Chip
          label={interaction.server_name}
          size="small"
          color={interaction.error_message ? 'error' : 'secondary'}
          variant="outlined"
          sx={{ fontSize: '0.75rem', fontWeight: 600 }}
        />
        {interaction.tool_name && !isToolList && (
          <Typography
            variant="body2"
            sx={{
              fontFamily: 'monospace',
              fontSize: '0.8rem',
              fontWeight: 600,
            }}
          >
            {interaction.tool_name}
          </Typography>
        )}
        {isToolList && (
          <Typography variant="body2" color="text.secondary" sx={{ fontSize: '0.8rem' }}>
            {getInteractionTypeLabel(MCP_INTERACTION_TYPE.TOOL_LIST)}
          </Typography>
        )}
      </Box>

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
