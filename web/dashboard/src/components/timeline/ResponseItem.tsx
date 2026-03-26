import { memo, useMemo } from 'react';
import { Box, Typography, Collapse, Chip, alpha } from '@mui/material';
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
import { MemoryCardList, type ParsedMemory } from './MemoryCardList';

interface ReflectorResult {
  create: Array<{ content: string; category: string; valence: string }>;
  reinforce: Array<{ memory_id: string }>;
  deprecate: Array<{ memory_id: string; reason: string }>;
}

function tryParseReflectorResult(content: string | undefined): ReflectorResult | null {
  if (!content) return null;
  try {
    const parsed = JSON.parse(content);
    if (Array.isArray(parsed.create) && Array.isArray(parsed.reinforce)) {
      return {
        create: parsed.create,
        reinforce: parsed.reinforce,
        deprecate: Array.isArray(parsed.deprecate) ? parsed.deprecate : [],
      };
    }
  } catch { /* not reflector JSON */ }
  return null;
}

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
  const reflectorResult = useMemo(() => isFinalAnalysis ? tryParseReflectorResult(item.content) : null, [isFinalAnalysis, item.content]);
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

    if (reflectorResult) {
      const created = reflectorResult.create || [];
      const reinforced = reflectorResult.reinforce || [];
      const deprecated = reflectorResult.deprecate || [];
      const totalActions = created.length + reinforced.length + deprecated.length;
      const tooltipText = totalActions === 0
        ? 'No new learnings'
        : `${created.length} new, ${reinforced.length} reinforced`;
      const createdMemories: ParsedMemory[] = created.map((m) => ({
        category: m.category || '',
        valence: m.valence || '',
        ageLabel: '',
        content: m.content,
      }));

      return (
        <Box
          data-flow-item-id={item.id}
          sx={{ mb: 3, mt: 3, display: 'flex', gap: 1.5, alignItems: 'flex-start', ...(shouldShowCollapsed && FADE_COLLAPSE_ANIMATION) }}
        >
          <EmojiIcon emoji="🧠" opacity={collapsedLeadingIconOpacity} showTooltip={shouldShowCollapsed} tooltipContent={tooltipText} tooltipType="final_answer" />
          <Box sx={{ flex: 1, minWidth: 0 }}>
            <CollapsibleItemHeader
              headerText="LESSONS LEARNED"
              headerColor="secondary.main"
              headerTextTransform="uppercase"
              shouldShowCollapsed={shouldShowCollapsed}
              collapsedHeaderOpacity={collapsedHeaderOpacity}
              onToggle={isCollapsible && onToggleAutoCollapse ? onToggleAutoCollapse : undefined}
            />
            <Collapse in={!shouldShowCollapsed} timeout={300}>
              <Box sx={{ mt: 1, pb: 3 }}>
                {totalActions === 0 ? (
                  <Typography variant="body2" color="text.secondary" sx={{ fontStyle: 'italic' }}>
                    No new learnings extracted from this investigation.
                  </Typography>
                ) : (
                  <>
                    {created.length > 0 && (
                      <Box sx={{ mb: 2 }}>
                        <Typography variant="caption" sx={{ fontWeight: 600, fontSize: '0.75rem', display: 'block', mb: 0.75 }}>
                          New Insights ({created.length})
                        </Typography>
                        <MemoryCardList memories={createdMemories} renderContent={(c) => c} />
                      </Box>
                    )}
                    {reinforced.length > 0 && (
                      <Box sx={(theme) => ({
                        mb: deprecated.length > 0 ? 2 : 0,
                        display: 'flex', alignItems: 'center', gap: 1,
                        p: 1, borderRadius: 1,
                        bgcolor: alpha(theme.palette.success.main, 0.06),
                        border: `1px solid ${alpha(theme.palette.success.main, 0.15)}`,
                      })}>
                        <Chip label={reinforced.length} size="small" color="success" variant="outlined" sx={{ fontWeight: 700, fontSize: '0.75rem', height: 22 }} />
                        <Typography variant="body2" color="text.secondary">
                          existing {reinforced.length === 1 ? 'insight' : 'insights'} reinforced
                        </Typography>
                      </Box>
                    )}
                    {deprecated.length > 0 && (
                      <Box>
                        <Typography variant="caption" sx={{ fontWeight: 600, fontSize: '0.75rem', display: 'block', mb: 0.75 }}>
                          Deprecated ({deprecated.length})
                        </Typography>
                        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.75 }}>
                          {deprecated.map((d, i) => (
                            <Box key={i} sx={(theme) => ({
                              p: 1, borderRadius: 1,
                              bgcolor: alpha(theme.palette.error.main, 0.04),
                              border: `1px solid ${alpha(theme.palette.error.main, 0.12)}`,
                            })}>
                              <Typography variant="body2" sx={{ fontSize: '0.8rem' }}>
                                <Typography component="span" sx={{ fontWeight: 600, fontSize: '0.7rem', color: 'text.secondary' }}>
                                  {d.memory_id}
                                </Typography>
                                {' — '}{d.reason}
                              </Typography>
                            </Box>
                          ))}
                        </Box>
                      </Box>
                    )}
                  </>
                )}
                {isCollapsible && onToggleAutoCollapse && <CollapseButton onClick={onToggleAutoCollapse} />}
              </Box>
            </Collapse>
          </Box>
        </Box>
      );
    }

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
            headerColor="success.main"
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
