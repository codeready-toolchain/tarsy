import { memo, useMemo } from 'react';
import { Box, Collapse, Typography, alpha } from '@mui/material';
import ReactMarkdown from 'react-markdown';
import EmojiIcon from '../shared/EmojiIcon';
import CollapsibleItemHeader from '../shared/CollapsibleItemHeader';
import CollapseButton from '../shared/CollapseButton';
import { hasMarkdownSyntax, remarkPlugins, thoughtMarkdownComponents } from '../../utils/markdownComponents';
import { FADE_COLLAPSE_ANIMATION } from '../../constants/chatFlowAnimations';
import { rehypeSearchHighlight } from '../../utils/rehypeSearchHighlight';
import { highlightSearchTermNodes } from '../../utils/search';
import CopyButton from '../shared/CopyButton';
import type { FlowItem } from '../../utils/timelineParser';

interface ToolSummaryItemProps {
  item: FlowItem;
  isAutoCollapsed?: boolean;
  onToggleAutoCollapse?: () => void;
  expandAll?: boolean;
  isCollapsible?: boolean;
  searchTerm?: string;
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
  searchTerm,
}: ToolSummaryItemProps) {
  const shouldShowCollapsed = isCollapsible && isAutoCollapsed && !expandAll;
  const collapsedHeaderOpacity = shouldShowCollapsed ? 0.65 : 1;
  const collapsedLeadingIconOpacity = shouldShowCollapsed ? 0.6 : 1;
  const hasMarkdown = hasMarkdownSyntax(item.content || '');
  const rehypePlugins = useMemo(
    () => { const p = rehypeSearchHighlight(searchTerm || ''); return p ? [p] : []; },
    [searchTerm],
  );

  return (
    <Box
      data-flow-item-id={item.id}
      sx={{
        mb: 1,
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
          headerColor="warning.main"
          headerTextTransform="uppercase"
          shouldShowCollapsed={shouldShowCollapsed}
          collapsedHeaderOpacity={collapsedHeaderOpacity}
          onToggle={isCollapsible && onToggleAutoCollapse ? onToggleAutoCollapse : undefined}
        />

        <Collapse in={!shouldShowCollapsed} timeout={300}>
          <Box sx={{ mt: 0.5 }}>
            <Box sx={{ display: 'flex', justifyContent: 'flex-end', mb: 0.5 }}>
              <CopyButton text={item.content || ''} variant="icon" size="small" tooltip="Copy summary" />
            </Box>
            <Box sx={(theme) => ({ pl: 3.5, ml: 3.5, py: 0.5, borderLeft: `2px solid ${alpha(theme.palette.warning.main, 0.2)}` })}>
              {hasMarkdown ? (
                <Box sx={{ '& p': { color: 'text.secondary' }, '& li': { color: 'text.secondary' }, color: 'text.secondary' }}>
                  <ReactMarkdown components={thoughtMarkdownComponents} remarkPlugins={remarkPlugins} rehypePlugins={rehypePlugins} skipHtml>
                    {item.content || ''}
                  </ReactMarkdown>
                </Box>
              ) : (
                <Typography
                  variant="body1"
                  sx={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word', lineHeight: 1.7, fontSize: '1rem', color: 'text.secondary' }}
                >
                  {searchTerm ? highlightSearchTermNodes(item.content, searchTerm) : item.content}
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
