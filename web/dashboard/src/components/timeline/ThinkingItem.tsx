import { memo } from 'react';
import { Box, Collapse } from '@mui/material';
import ReactMarkdown from 'react-markdown';
import EmojiIcon from '../shared/EmojiIcon';
import CollapsibleItemHeader from '../shared/CollapsibleItemHeader';
import CollapseButton from '../shared/CollapseButton';
import ContentCard from '../shared/ContentCard';
import { remarkPlugins, thoughtMarkdownComponents } from '../../utils/markdownComponents';
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
 * Content is rendered in italic / text.secondary style.
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
        tooltipType="thought"
      />

      <Box sx={{ flex: 1, minWidth: 0 }}>
        <CollapsibleItemHeader
          headerText={
            typeof item.metadata?.duration_ms === 'number' && item.metadata.duration_ms > 0
              ? `Thought for ${formatDurationMs(item.metadata.duration_ms)}`
              : 'Thought'
          }
          headerColor="info.main"
          shouldShowCollapsed={shouldShowCollapsed}
          collapsedHeaderOpacity={collapsedHeaderOpacity}
          onToggle={isCollapsible && onToggleAutoCollapse ? onToggleAutoCollapse : undefined}
        />

        <Collapse in={!shouldShowCollapsed} timeout={300}>
          <Box sx={{ mt: 0.5 }}>
            <ContentCard maxHeight="900px">
              <Box
                sx={{
                  '& p, & li': { color: 'text.secondary', fontStyle: 'italic' },
                  color: 'text.secondary',
                  fontStyle: 'italic',
                }}
              >
                <ReactMarkdown components={thoughtMarkdownComponents} remarkPlugins={remarkPlugins} skipHtml>
                  {item.content || ''}
                </ReactMarkdown>
              </Box>
            </ContentCard>
            {isCollapsible && onToggleAutoCollapse && <CollapseButton onClick={onToggleAutoCollapse} />}
          </Box>
        </Collapse>
      </Box>
    </Box>
  );
}

export default memo(ThinkingItem);
