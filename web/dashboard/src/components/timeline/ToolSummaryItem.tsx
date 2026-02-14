import { memo } from 'react';
import { Box, Collapse } from '@mui/material';
import ReactMarkdown from 'react-markdown';
import remarkBreaks from 'remark-breaks';
import EmojiIcon from '../shared/EmojiIcon';
import CollapsibleItemHeader from '../shared/CollapsibleItemHeader';
import CollapseButton from '../shared/CollapseButton';
import { hasMarkdownSyntax, thoughtMarkdownComponents } from '../../utils/markdownComponents';
import { FADE_COLLAPSE_ANIMATION } from '../../constants/chatFlowAnimations';
import type { FlowItem } from '../../utils/timelineParser';
import { Typography } from '@mui/material';

interface ToolSummaryItemProps {
  item: FlowItem;
  isAutoCollapsed?: boolean;
  onToggleAutoCollapse?: () => void;
  expandAll?: boolean;
  isCollapsible?: boolean;
}

/**
 * ToolSummaryItem - renders mcp_tool_summary timeline events.
 * Amber-bordered block with "TOOL RESULT SUMMARY" header and collapsible content.
 */
function ToolSummaryItem({
  item,
  isAutoCollapsed = false,
  onToggleAutoCollapse,
  expandAll = false,
  isCollapsible = true,
}: ToolSummaryItemProps) {
  const shouldShowCollapsed = isCollapsible && isAutoCollapsed && !expandAll;
  const collapsedHeaderOpacity = shouldShowCollapsed ? 0.65 : 1;
  const collapsedLeadingIconOpacity = shouldShowCollapsed ? 0.6 : 1;
  const hasMarkdown = hasMarkdownSyntax(item.content || '');

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
        emoji="📋"
        opacity={collapsedLeadingIconOpacity}
        showTooltip={shouldShowCollapsed}
        tooltipContent={item.content || ''}
        tooltipType="summarization"
      />

      <Box sx={{ flex: 1, minWidth: 0 }}>
        <CollapsibleItemHeader
          headerText="TOOL RESULT SUMMARY"
          headerColor="rgba(237, 108, 2, 0.9)"
          headerTextTransform="uppercase"
          shouldShowCollapsed={shouldShowCollapsed}
          collapsedHeaderOpacity={collapsedHeaderOpacity}
          onToggle={isCollapsible && onToggleAutoCollapse ? onToggleAutoCollapse : undefined}
        />

        <Collapse in={!shouldShowCollapsed} timeout={300}>
          <Box sx={{ mt: 0.5 }}>
            <Box sx={{ pl: 3.5, ml: 3.5, py: 0.5, borderLeft: '2px solid rgba(237, 108, 2, 0.2)' }}>
              {hasMarkdown ? (
                <Box sx={{ '& p': { color: 'text.secondary' }, '& li': { color: 'text.secondary' }, color: 'text.secondary' }}>
                  <ReactMarkdown components={thoughtMarkdownComponents} remarkPlugins={[remarkBreaks]} skipHtml>
                    {item.content || ''}
                  </ReactMarkdown>
                </Box>
              ) : (
                <Typography
                  variant="body1"
                  sx={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word', lineHeight: 1.7, fontSize: '1rem', color: 'text.secondary' }}
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

export default memo(ToolSummaryItem);
