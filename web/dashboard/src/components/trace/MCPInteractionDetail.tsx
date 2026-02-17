/**
 * MCPInteractionDetail — full expanded view of an MCP interaction.
 *
 * Shows tool call (name + arguments), result or available tools,
 * error section, server info, and copy buttons.
 *
 * Visual pattern from old dashboard's InteractionDetails.tsx (renderMCPDetails),
 * data layer rewritten for MCPInteractionDetailResponse.
 */

import { memo } from 'react';
import { Box, Typography, Stack, Divider } from '@mui/material';

import type { MCPInteractionDetailResponse } from '../../types/trace';
import CopyButton from '../shared/CopyButton';
import JsonDisplay from '../shared/JsonDisplay';
import { formatMCPDetailForCopy } from './traceHelpers';

interface MCPInteractionDetailProps {
  detail: MCPInteractionDetailResponse;
}

function MCPInteractionDetail({ detail }: MCPInteractionDetailProps) {
  const isToolList =
    detail.interaction_type === 'tool_list' ||
    (detail.interaction_type === 'tool_call' && detail.tool_name === 'list_tools');

  const rawCopyText = (() => {
    if (isToolList) {
      return `Tool List from ${detail.server_name}\n\n---\n\n${JSON.stringify(detail.available_tools, null, 2)}`;
    }
    return `${detail.tool_name}(${JSON.stringify(detail.tool_arguments, null, 2)})\n\n---\n\n${JSON.stringify(detail.tool_result, null, 2)}`;
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
                  MCP Error
                </Box>
              </Box>
              <CopyButton
                text={detail.error_message}
                variant="icon"
                size="small"
                tooltip="Copy error message"
              />
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

        {/* Tool Call section — only for actual tool calls, not tool lists */}
        {!isToolList && (
          <Box>
            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 1 }}>
              <Typography variant="subtitle2" sx={{ fontWeight: 600 }}>
                Tool Call
              </Typography>
              <CopyButton
                text={`${detail.tool_name}(${JSON.stringify(detail.tool_arguments, null, 2)})`}
                variant="icon"
                size="small"
                tooltip="Copy tool call"
              />
            </Box>
            <Box>
              <Typography
                variant="body2"
                sx={{
                  fontFamily: 'monospace',
                  fontSize: '0.875rem',
                  fontWeight: 600,
                  mb: 1,
                  p: 1,
                  bgcolor: 'grey.100',
                  borderRadius: 1,
                }}
              >
                {detail.tool_name}
              </Typography>
              {detail.tool_arguments && Object.keys(detail.tool_arguments).length > 0 && (
                <JsonDisplay data={detail.tool_arguments} />
              )}
            </Box>
          </Box>
        )}

        {/* Result / Available Tools */}
        <Box>
          <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 1 }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
              <Typography variant="subtitle2" sx={{ fontWeight: 600 }}>
                {isToolList ? 'Available Tools' : 'Result'}
              </Typography>
              {isToolList && detail.available_tools && (
                <Typography
                  variant="caption"
                  color="text.secondary"
                  sx={{
                    bgcolor: 'primary.main',
                    color: 'primary.contrastText',
                    px: 1,
                    py: 0.25,
                    borderRadius: 1,
                    fontWeight: 600,
                    fontSize: '0.75rem',
                  }}
                >
                  {detail.available_tools.length} tools
                </Typography>
              )}
            </Box>
            <CopyButton
              text={JSON.stringify(isToolList ? detail.available_tools : detail.tool_result, null, 2)}
              variant="icon"
              size="small"
              tooltip={isToolList ? 'Copy available tools' : 'Copy result'}
            />
          </Box>
          <JsonDisplay
            data={isToolList ? detail.available_tools : detail.tool_result}
            maxHeight={800}
          />
        </Box>

        {/* Tool metadata */}
        <Box>
          <Typography variant="subtitle2" sx={{ fontWeight: 600, mb: 1 }}>
            Tool Information
          </Typography>
          <Stack direction="row" spacing={2} flexWrap="wrap">
            <Typography variant="body2" color="text.secondary">
              <strong>Server:</strong> {detail.server_name}
            </Typography>
            {detail.tool_name && (
              <Typography variant="body2" color="text.secondary">
                <strong>Tool:</strong> {detail.tool_name}
              </Typography>
            )}
          </Stack>
        </Box>

        {/* Copy buttons */}
        <Box sx={{ mt: 2, display: 'flex', gap: 1, justifyContent: 'flex-start' }}>
          <CopyButton
            text={formatMCPDetailForCopy(detail)}
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

export default memo(MCPInteractionDetail);
