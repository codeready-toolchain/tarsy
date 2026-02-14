import { memo } from 'react';
import { Box, Typography, Collapse, alpha } from '@mui/material';
import ReactMarkdown from 'react-markdown';
import remarkBreaks from 'remark-breaks';
import EmojiIcon from '../shared/EmojiIcon';
import CollapsibleItemHeader from '../shared/CollapsibleItemHeader';
import CollapseButton from '../shared/CollapseButton';
import { hasMarkdownSyntax, thoughtMarkdownComponents } from '../../utils/markdownComponents';
import { FADE_COLLAPSE_ANIMATION } from '../../constants/chatFlowAnimations';
import { formatDurationMs } from '../../utils/format';
import type { FlowItem } from '../../utils/timelineParser';

interface ThinkingItemProps {
  item: FlowItem;
  isAutoCollapsed?: boolean;
  onToggleAutoCollapse?: () => void;
  expandAll?: boolean;
  isCollapsible?: boolean;
}

/**
 * ThinkingItem - renders llm_thinking timeline events.
 * Collapsible grey box with brain emoji and "Thought" header.
 */
function ThinkingItem({
  item,
  isAutoCollapsed = false,
  onToggleAutoCollapse,
  expandAll = false,
  isCollapsible = true,
}: ThinkingItemProps) {
  const shouldShowCollapsed = isCollapsible && isAutoCollapsed && !expandAll;
  const collapsedHeaderOpacity = shouldShowCollapsed ? 0.65 : 1;
  const collapsedLeadingIconOpacity = shouldShowCollapsed ? 0.6 : 1;
  const hasMarkdown = hasMarkdownSyntax(item.content || '');

  // Check for native thinking (via metadata flag)
  const isNativeThinking = !!item.metadata?.is_native_thinking;

  return (
    <Box
      sx={{
        mb: 1.5,
        display: 'flex',
        gap: 1.5,
        alignItems: 'flex-start',
        ...(shouldShowCollapsed && FADE_COLLAPSE_ANIMATION),
      }}
    >
      <EmojiIcon
        emoji="💭"
        opacity={collapsedLeadingIconOpacity}
        showTooltip={shouldShowCollapsed}
        tooltipContent={item.content || ''}
        tooltipType={isNativeThinking ? 'native_thinking' : 'thought'}
      />

      <Box sx={{ flex: 1, minWidth: 0 }}>
        <CollapsibleItemHeader
          headerText={
            (item.metadata?.duration_ms as number) > 0
              ? `Thought for ${formatDurationMs(item.metadata!.duration_ms as number)}`
              : 'Thought'
          }
          headerColor="info.main"
          shouldShowCollapsed={shouldShowCollapsed}
          collapsedHeaderOpacity={collapsedHeaderOpacity}
          onToggle={isCollapsible && onToggleAutoCollapse ? onToggleAutoCollapse : undefined}
        />

        <Collapse in={!shouldShowCollapsed} timeout={300}>
          <Box sx={{ mt: 0.5 }}>
            <Box
              sx={(theme) => ({
                bgcolor: alpha(theme.palette.grey[300], 0.15),
                border: '1px solid',
                borderColor: alpha(theme.palette.grey[400], 0.2),
                borderRadius: 1,
                p: 1.5,
              })}
            >
              {hasMarkdown ? (
                <Box
                  sx={
                    isNativeThinking
                      ? { '& p, & li': { color: 'text.secondary', fontStyle: 'italic' }, color: 'text.secondary', fontStyle: 'italic' }
                      : { color: 'text.primary' }
                  }
                >
                  <ReactMarkdown components={thoughtMarkdownComponents} remarkPlugins={[remarkBreaks]} skipHtml>
                    {item.content || ''}
                  </ReactMarkdown>
                </Box>
              ) : (
                <Typography
                  variant="body1"
                  sx={{
                    whiteSpace: 'pre-wrap',
                    wordBreak: 'break-word',
                    lineHeight: 1.7,
                    fontSize: '1rem',
                    color: isNativeThinking ? 'text.secondary' : 'text.primary',
                    fontStyle: isNativeThinking ? 'italic' : 'normal',
                  }}
                >
                  {item.content}
                </Typography>
              )}
            </Box>
            {isCollapsible && onToggleAutoCollapse && <CollapseButton onClick={onToggleAutoCollapse} />}
          </Box>
        </Collapse>
      </Box>
    </Box>
  );
}

export default memo(ThinkingItem);
