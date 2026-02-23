import { useState, useEffect } from 'react';
import { Box, Typography, Collapse, IconButton, alpha } from '@mui/material';
import { ExpandMore, ExpandLess, CheckCircle, Error as ErrorIcon, InfoOutlined } from '@mui/icons-material';
import JsonDisplay from '../shared/JsonDisplay';
import CopyButton from '../shared/CopyButton';
import { formatDurationMs } from '../../utils/format';
import type { FlowItem } from '../../utils/timelineParser';
import { EXECUTION_STATUS } from '../../constants/sessionStatus';

interface ToolCallItemProps {
  item: FlowItem;
  expandAll?: boolean;
}

/**
 * Check if arguments are simple (flat key-value pairs with primitive values)
 */
const isSimpleArguments = (args: Record<string, unknown> | null): boolean => {
  if (!args || typeof args !== 'object' || Array.isArray(args)) return false;
  const keys = Object.keys(args);
  if (keys.length === 0) return false;
  return keys.every(key => {
    const value = args[key];
    const type = typeof value;
    if (value === null || type === 'string' || type === 'number' || type === 'boolean') return true;
    if (Array.isArray(value)) {
      return value.length <= 5 && value.every(item => {
        const itemType = typeof item;
        return item === null || itemType === 'string' || itemType === 'number' || itemType === 'boolean';
      });
    }
    return false;
  });
};

const SimpleArgumentsList = ({ args }: { args: Record<string, unknown> }) => (
  <Box
    sx={(theme) => ({
      bgcolor: theme.palette.grey[50], borderRadius: 1,
      border: `1px solid ${theme.palette.divider}`, p: 1.5,
      fontFamily: 'monospace', fontSize: '0.875rem'
    })}
  >
    {Object.entries(args).map(([key, value], index) => (
      <Box key={key} sx={{ display: 'flex', mb: index < Object.keys(args).length - 1 ? 0.75 : 0, alignItems: 'flex-start' }}>
        <Typography component="span" sx={(theme) => ({ fontFamily: 'monospace', fontSize: '0.875rem', fontWeight: 600, color: theme.palette.primary.main, mr: 1, minWidth: '100px', flexShrink: 0 })}>
          {key}:
        </Typography>
        <Typography component="span" sx={{ fontFamily: 'monospace', fontSize: '0.875rem', color: 'text.primary', wordBreak: 'break-word', whiteSpace: 'pre-wrap' }}>
          {Array.isArray(value) ? `[${(value as unknown[]).map(v => typeof v === 'string' ? `"${v}"` : String(v)).join(', ')}]` : typeof value === 'string' ? `"${value}"` : String(value)}
        </Typography>
      </Box>
    ))}
  </Box>
);

/**
 * ToolCallItem - renders llm_tool_call timeline events.
 * Expandable box showing tool name, arguments preview, duration, and result.
 */
function ToolCallItem({ item, expandAll = false }: ToolCallItemProps) {
  const [expanded, setExpanded] = useState(false);
  useEffect(() => {
    setExpanded(expandAll);
  }, [expandAll]);
  const isExpanded = expandAll || expanded;

  // Extract data from FlowItem metadata
  const toolName = (item.metadata?.tool_name as string) || 'unknown';
  const serverName = (item.metadata?.server_name as string) || 'unknown';
  // Arguments may be stored as a parsed object or as a JSON string in metadata.
  // Parse strings into objects so isSimpleArguments / SimpleArgumentsList work correctly.
  const toolArguments: Record<string, unknown> = (() => {
    const raw = item.metadata?.arguments;
    if (!raw) return {};
    if (typeof raw === 'object' && !Array.isArray(raw)) return raw as Record<string, unknown>;
    if (typeof raw === 'string') {
      try { return JSON.parse(raw) as Record<string, unknown>; } catch { return {}; }
    }
    return {};
  })();
  const errorMessage = (item.metadata?.error_message as string) || '';
  const durationMs = item.metadata?.duration_ms as number | null | undefined;
  // is_error = tool returned an error result (business logic, e.g. "not found")
  // This is NOT an MCP failure — the tool executed fine and returned a response.
  const isToolResultError = !!item.metadata?.is_error;
  // MCP-level failure: the tool call itself failed (bad args, timeout, unknown tool, etc.)
  const isMcpFailure = item.status === EXECUTION_STATUS.FAILED || !!errorMessage;
  // Tool result is in item.content (after completion)
  const toolResult = item.content || null;

  const getArgumentsPreview = (): string => {
    if (!toolArguments || typeof toolArguments !== 'object') return '';
    const keys = Object.keys(toolArguments);
    if (keys.length === 0) return '(no arguments)';
    const previewKeys = keys.slice(0, 2);
    const preview = previewKeys.map(key => {
      const value = toolArguments[key];
      const valueStr = typeof value === 'string' ? value : JSON.stringify(value);
      const truncated = valueStr.length > 25 ? valueStr.substring(0, 25) + '...' : valueStr;
      return `${key}: ${truncated}`;
    }).join(', ');
    return keys.length > 2 ? `${preview}, ...` : preview;
  };

  // Three-tier visual: green (success), amber (tool returned error result), red (MCP failure)
  const StatusIcon = isMcpFailure ? ErrorIcon : isToolResultError ? InfoOutlined : CheckCircle;
  const accentKey: 'error' | 'warning' | 'primary' = isMcpFailure ? 'error' : isToolResultError ? 'warning' : 'primary';

  return (
    <Box
      sx={(theme) => ({
        ml: 4, my: 1, mr: 1,
        border: '2px solid',
        borderColor: alpha(theme.palette[accentKey].main, 0.5),
        borderRadius: 1.5,
        bgcolor: alpha(theme.palette[accentKey].main, 0.08),
        boxShadow: `0 1px 3px ${alpha(theme.palette.common.black, 0.08)}`
      })}
    >
      <Box
        sx={(theme) => ({
          display: 'flex', alignItems: 'center', gap: 1, px: 1.5, py: 0.75,
          cursor: 'pointer', borderRadius: 1.5, transition: 'background-color 0.2s ease',
          '&:hover': { bgcolor: alpha(theme.palette[accentKey].main, 0.2) }
        })}
        onClick={() => {
          if (expandAll) return;
          setExpanded((prev) => !prev);
        }}
      >
        <StatusIcon sx={(theme) => ({ fontSize: 18, color: theme.palette[accentKey].main })} />
        <Typography variant="body2" sx={(theme) => ({ fontFamily: 'monospace', fontWeight: 600, fontSize: '0.9rem', color: theme.palette[accentKey].main })}>
          {toolName}
        </Typography>
        <Typography variant="caption" color="text.secondary" sx={{ fontSize: '0.8rem', flex: 1, lineHeight: 1.4 }}>
          {getArgumentsPreview()}
        </Typography>
        {durationMs != null && (
          <Typography variant="caption" color="text.secondary" sx={{ fontSize: '0.75rem' }}>
            {formatDurationMs(durationMs)}
          </Typography>
        )}
        <IconButton size="small" sx={{ p: 0.25 }}>
          {isExpanded ? <ExpandLess fontSize="small" /> : <ExpandMore fontSize="small" />}
        </IconButton>
      </Box>

      <Collapse in={isExpanded}>
        <Box sx={{ px: 1.5, pb: 1.5, pt: 0.5, borderTop: 1, borderColor: 'divider' }}>
          <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mb: 1 }}>
            Server: {serverName}
          </Typography>

          {isMcpFailure && errorMessage && (
            <Box sx={(theme) => ({ mb: 1, p: 1, bgcolor: alpha(theme.palette.error.main, 0.1), borderRadius: 1, border: `1px solid ${alpha(theme.palette.error.main, 0.3)}` })}>
              <Typography variant="caption" sx={(theme) => ({ fontWeight: 600, color: theme.palette.error.dark, fontSize: '0.8rem' })}>
                Error: {errorMessage}
              </Typography>
            </Box>
          )}

          <Box sx={{ mb: 1 }}>
            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 0.5 }}>
              <Typography variant="caption" sx={{ fontWeight: 600, fontSize: '0.75rem' }}>Arguments</Typography>
              <CopyButton text={JSON.stringify(toolArguments, null, 2)} variant="icon" size="small" tooltip="Copy arguments" />
            </Box>
            {toolArguments && Object.keys(toolArguments).length > 0 ? (
              isSimpleArguments(toolArguments) ? <SimpleArgumentsList args={toolArguments} /> : <JsonDisplay data={toolArguments} maxHeight={250} />
            ) : (
              <Typography variant="caption" color="text.secondary" sx={{ fontStyle: 'italic' }}>No arguments</Typography>
            )}
          </Box>

          <Box>
            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 0.5 }}>
              <Typography variant="caption" sx={{ fontWeight: 600, fontSize: '0.75rem' }}>Result</Typography>
              <CopyButton text={typeof toolResult === 'string' ? toolResult : JSON.stringify(toolResult, null, 2)} variant="icon" size="small" tooltip="Copy result" />
            </Box>
            {toolResult ? <JsonDisplay data={toolResult} maxHeight={300} /> : (
              <Typography variant="caption" color="text.secondary" sx={{ fontStyle: 'italic' }}>No result</Typography>
            )}
          </Box>
        </Box>
      </Collapse>
    </Box>
  );
}

export default ToolCallItem;
