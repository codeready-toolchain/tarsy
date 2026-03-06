import { memo, useMemo } from 'react';
import { Box, Typography, Collapse } from '@mui/material';
import ReactMarkdown, { defaultUrlTransform } from 'react-markdown';
import EmojiIcon from '../shared/EmojiIcon';
import CollapsibleItemHeader from '../shared/CollapsibleItemHeader';
import CollapseButton from '../shared/CollapseButton';
import ContentCard from '../shared/ContentCard';
import { hasMarkdownSyntax, remarkPlugins, thoughtMarkdownComponents } from '../../utils/markdownComponents';
import { FADE_COLLAPSE_ANIMATION } from '../../constants/chatFlowAnimations';
import { FLOW_ITEM, type FlowItem } from '../../utils/timelineParser';
import { rehypeSearchHighlight } from '../../utils/rehypeSearchHighlight';
import { highlightSearchTermNodes } from '../../utils/search';

interface ResponseItemProps {
  item: FlowItem;
  isAutoCollapsed?: boolean;
  onToggleAutoCollapse?: () => void;
  expandAll?: boolean;
  isCollapsible?: boolean;
  searchTerm?: string;
}

/**
 * ResponseItem - renders llm_response and final_analysis timeline events.
 * For final_analysis: green "FINAL ANSWER" header with target emoji.
 * For llm_response: simple message bubble with speech emoji.
 */
function ResponseItem({
  item,
  isAutoCollapsed = false,
  onToggleAutoCollapse,
  expandAll = false,
  isCollapsible = false,
  searchTerm,
}: ResponseItemProps) {
  const isFinalAnalysis = item.type === FLOW_ITEM.FINAL_ANALYSIS;
  const isForcedConclusion = !!item.metadata?.forced_conclusion;
  const hasMarkdown = hasMarkdownSyntax(item.content || '');
  const rehypePlugins = useMemo(
    () => { const p = rehypeSearchHighlight(searchTerm || ''); return p ? [p] : []; },
    [searchTerm],
  );

  const renderContent = () => {
    if (hasMarkdown) {
      return (
        <Box sx={{ color: 'text.primary' }}>
          <ReactMarkdown
            urlTransform={defaultUrlTransform}
            components={thoughtMarkdownComponents}
            remarkPlugins={remarkPlugins}
            rehypePlugins={rehypePlugins}
            skipHtml
          >
            {item.content || ''}
          </ReactMarkdown>
        </Box>
      );
    }
    return (
      <Typography
        variant="body1"
        sx={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word', lineHeight: 1.7, fontSize: '1rem', color: 'text.primary' }}
      >
        {searchTerm ? highlightSearchTermNodes(item.content, searchTerm) : item.content}
      </Typography>
    );
  };

  // Final analysis / forced conclusion rendering
  if (isFinalAnalysis) {
    const shouldShowCollapsed = isCollapsible && isAutoCollapsed && !expandAll;
    const collapsedHeaderOpacity = shouldShowCollapsed ? 0.65 : 1;
    const collapsedLeadingIconOpacity = shouldShowCollapsed ? 0.6 : 1;
    const headerText = isForcedConclusion ? 'FINAL ANSWER (⚠️Max Iterations)' : 'FINAL ANSWER';

    return (
      <Box
        data-flow-item-id={item.id}
        sx={{
          mb: 3,
          mt: 3,
          display: 'flex',
          gap: 1.5,
          alignItems: 'flex-start',
          ...(shouldShowCollapsed && FADE_COLLAPSE_ANIMATION),
        }}
      >
        <EmojiIcon
          emoji="🎯"
          opacity={collapsedLeadingIconOpacity}
          showTooltip={shouldShowCollapsed}
          tooltipContent={item.content || ''}
          tooltipType={isForcedConclusion ? 'forced_conclusion' : 'final_answer'}
        />
        <Box sx={{ flex: 1, minWidth: 0 }}>
          <CollapsibleItemHeader
            headerText={headerText}
            headerColor="#2e7d32"
            headerTextTransform="uppercase"
            shouldShowCollapsed={shouldShowCollapsed}
            collapsedHeaderOpacity={collapsedHeaderOpacity}
            onToggle={isCollapsible && onToggleAutoCollapse ? onToggleAutoCollapse : undefined}
          />
          <Collapse in={!shouldShowCollapsed} timeout={300}>
            <Box sx={{ mt: 0.5, pb: 3 }}>
              {renderContent()}
              {isCollapsible && onToggleAutoCollapse && <CollapseButton onClick={onToggleAutoCollapse} />}
            </Box>
          </Collapse>
        </Box>
      </Box>
    );
  }

  // Regular llm_response: card-based rendering with fixed height
  if (item.type === FLOW_ITEM.RESPONSE) {
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
          emoji="💬"
          opacity={collapsedLeadingIconOpacity}
          showTooltip={shouldShowCollapsed}
          tooltipContent={item.content || ''}
          tooltipType="response"
          onClick={isCollapsible && onToggleAutoCollapse ? onToggleAutoCollapse : undefined}
        />
        <Box sx={{ flex: 1, minWidth: 0 }}>
          <CollapsibleItemHeader
            headerText={shouldShowCollapsed
              ? (() => {
                  const raw = (item.content || '').trim();
                  const firstLine = raw.split('\n')[0];
                  return firstLine.length > 120 ? firstLine.slice(0, 120) + '…' : firstLine;
                })()
              : 'Response'}
            headerColor={shouldShowCollapsed ? 'text.secondary' : 'primary.main'}
            shouldShowCollapsed={shouldShowCollapsed}
            collapsedHeaderOpacity={collapsedHeaderOpacity}
            onToggle={isCollapsible && onToggleAutoCollapse ? onToggleAutoCollapse : undefined}
          />
          <Collapse in={!shouldShowCollapsed} timeout={300}>
            <Box sx={{ mt: 0.5 }}>
              <ContentCard maxHeight="900px" copyText={item.content || ''}>
                {renderContent()}
              </ContentCard>
              {isCollapsible && onToggleAutoCollapse && <CollapseButton onClick={onToggleAutoCollapse} />}
            </Box>
          </Collapse>
        </Box>
      </Box>
    );
  }

  // Executive summary / other non-final-analysis types (no card)
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
        emoji="💬"
        opacity={collapsedLeadingIconOpacity}
        showTooltip={shouldShowCollapsed}
        tooltipContent={item.content || ''}
        tooltipType="response"
        onClick={isCollapsible && onToggleAutoCollapse ? onToggleAutoCollapse : undefined}
      />
      <Box sx={{ flex: 1, minWidth: 0 }}>
        {shouldShowCollapsed && (
          <CollapsibleItemHeader
            headerText={(() => {
              const raw = (item.content || '').trim();
              const firstLine = raw.split('\n')[0];
              return firstLine.length > 120 ? firstLine.slice(0, 120) + '…' : firstLine;
            })()}
            headerColor="text.secondary"
            shouldShowCollapsed={shouldShowCollapsed}
            collapsedHeaderOpacity={collapsedHeaderOpacity}
            onToggle={isCollapsible && onToggleAutoCollapse ? onToggleAutoCollapse : undefined}
          />
        )}
        <Collapse in={!shouldShowCollapsed} timeout={300}>
          <Box sx={{ mt: 0.5 }}>
            {renderContent()}
            {isCollapsible && onToggleAutoCollapse && <CollapseButton onClick={onToggleAutoCollapse} />}
          </Box>
        </Collapse>
      </Box>
    </Box>
  );
}

export default memo(ResponseItem);
