import { memo, useMemo } from 'react';
import { Box, Collapse } from '@mui/material';
import ReactMarkdown from 'react-markdown';
import EmojiIcon from '../shared/EmojiIcon';
import CollapsibleItemHeader from '../shared/CollapsibleItemHeader';
import CollapseButton from '../shared/CollapseButton';
import ContentCard from '../shared/ContentCard';
import { remarkPlugins, thoughtMarkdownComponents } from '../../utils/markdownComponents';
import { FADE_COLLAPSE_ANIMATION } from '../../constants/chatFlowAnimations';
import { rehypeSearchHighlight } from '../../utils/rehypeSearchHighlight';
import type { FlowItem } from '../../utils/timelineParser';

interface SkillLoadedItemProps {
  item: FlowItem;
  isAutoCollapsed?: boolean;
  onToggleAutoCollapse?: () => void;
  expandAll?: boolean;
  isCollapsible?: boolean;
  searchTerm?: string;
}

function SkillLoadedItem({
  item,
  isAutoCollapsed = false,
  onToggleAutoCollapse,
  expandAll = false,
  isCollapsible = true,
  searchTerm,
}: SkillLoadedItemProps) {
  const rehypePlugins = useMemo(
    () => { const p = rehypeSearchHighlight(searchTerm || ''); return p ? [p] : []; },
    [searchTerm],
  );
  const skillName = (item.metadata?.skill_name as string) || 'Skill';
  const shouldShowCollapsed = isCollapsible && isAutoCollapsed && !expandAll;
  const collapsedHeaderOpacity = shouldShowCollapsed ? 0.65 : 1;
  const collapsedLeadingIconOpacity = shouldShowCollapsed ? 0.6 : 1;

  return (
    <Box
      data-flow-item-id={item.id}
      sx={{
        mb: 1.5,
        display: 'flex',
        gap: 1.5,
        alignItems: 'flex-start',
        ...(shouldShowCollapsed && FADE_COLLAPSE_ANIMATION),
      }}
    >
      <EmojiIcon
        emoji="📖"
        opacity={collapsedLeadingIconOpacity}
        showTooltip={shouldShowCollapsed}
        tooltipContent={`Pre-loaded skill: ${skillName}`}
        tooltipType="thought"
      />

      <Box sx={{ flex: 1, minWidth: 0 }}>
        <CollapsibleItemHeader
          headerText={`Pre-loaded Skill: ${skillName}`}
          headerColor="info.main"
          shouldShowCollapsed={shouldShowCollapsed}
          collapsedHeaderOpacity={collapsedHeaderOpacity}
          onToggle={isCollapsible && onToggleAutoCollapse ? onToggleAutoCollapse : undefined}
        />

        <Collapse in={!shouldShowCollapsed} timeout={300}>
          <Box sx={{ mt: 0.5 }}>
            <ContentCard maxHeight="600px" copyText={item.content || ''}>
              <Box sx={{ color: 'text.secondary' }}>
                <ReactMarkdown components={thoughtMarkdownComponents} remarkPlugins={remarkPlugins} rehypePlugins={rehypePlugins} skipHtml>
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

export default memo(SkillLoadedItem);
