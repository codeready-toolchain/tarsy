import { memo, useEffect, useRef } from 'react';
import { Box, Typography, alpha } from '@mui/material';
import ReactMarkdown from 'react-markdown';
import TypewriterText from './TypewriterText';
import ContentCard from '../shared/ContentCard';
import { TIMELINE_EVENT_TYPES } from '../../constants/eventTypes';
import { LLM_INTERACTION_TYPE } from '../../constants/interactionTypes';
import { getFinalAnalysisPresentation } from '../timeline/ResponseItem';
import { TOOL_TYPE, MEMORY_TOOL_NAME } from '../../constants/toolTypes';
import { getSkillNamesLabel } from '../../utils/format';
import { getToolVisualConfig } from '../../utils/toolCallVisual';
import { thoughtMarkdownComponents, remarkPlugins } from '../../utils/markdownComponents';

/**
 * StreamingItem for the streaming content renderer.
 * Maps to a streaming timeline event with type and content.
 */
export interface StreamingItem {
  eventType: string;
  content: string;
  metadata?: Record<string, unknown>;
  collapsing?: boolean;
}

interface StreamingContentRendererProps {
  item: StreamingItem;
  stageType?: string;
}

// --- ThinkingBlock ---
// Renders streaming thought content in italic / text.secondary style
// (matching completed ThinkingItem).

const ThinkingBlock = memo(({ content }: { content: string }) => {
  const scrollContainerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (scrollContainerRef.current) {
      scrollContainerRef.current.scrollTop = scrollContainerRef.current.scrollHeight;
    }
  }, [content]);

  return (
    <Box sx={{ mb: 1.5, display: 'flex', gap: 1.5 }}>
      <Typography variant="body2" sx={{ fontSize: '1.1rem', lineHeight: 1, flexShrink: 0, mt: 0.25 }}>
        💭
      </Typography>
      <Box sx={{ flex: 1, minWidth: 0 }}>
        <Typography
          variant="caption"
          sx={{
            fontWeight: 700, textTransform: 'none', letterSpacing: 0.5,
            fontSize: '0.75rem', color: 'info.main', display: 'block', mb: 0.5
          }}
        >
          Thinking...
        </Typography>
        <TypewriterText text={content}>
          {(displayText) => {
            if (!displayText) return null;
            return (
              <ContentCard ref={scrollContainerRef} height="150px">
                <Box
                  sx={{
                    '& p, & li': { color: 'text.secondary', fontStyle: 'italic' },
                    color: 'text.secondary',
                    fontStyle: 'italic',
                  }}
                >
                  <ReactMarkdown components={thoughtMarkdownComponents} remarkPlugins={remarkPlugins} skipHtml>
                    {displayText}
                  </ReactMarkdown>
                </Box>
              </ContentCard>
            );
          }}
        </TypewriterText>
      </Box>
    </Box>
  );
});

ThinkingBlock.displayName = 'ThinkingBlock';

// --- ResponseBlock ---
// Renders streaming llm_response content in a card/box with auto-scroll,
// matching the completed ResponseItem card style.

const ResponseBlock = memo(({ content }: { content: string }) => {
  const scrollContainerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (scrollContainerRef.current) {
      scrollContainerRef.current.scrollTop = scrollContainerRef.current.scrollHeight;
    }
  }, [content]);

  return (
    <Box sx={{ mb: 1.5, display: 'flex', gap: 1.5 }}>
      <Typography variant="body2" sx={{ fontSize: '1.1rem', lineHeight: 1, flexShrink: 0, mt: 0.25 }}>
        💬
      </Typography>
      <Box sx={{ flex: 1, minWidth: 0 }}>
        <Typography
          variant="caption"
          sx={{
            fontWeight: 700, textTransform: 'none', letterSpacing: 0.5,
            fontSize: '0.75rem', color: 'primary.main', display: 'block', mb: 0.5
          }}
        >
          Responding...
        </Typography>
        <TypewriterText text={content}>
          {(displayText) => {
            if (!displayText) return null;
            return (
              <ContentCard ref={scrollContainerRef} height="150px">
                <Box sx={{ color: 'text.primary' }}>
                  <ReactMarkdown components={thoughtMarkdownComponents} remarkPlugins={remarkPlugins} skipHtml>
                    {displayText}
                  </ReactMarkdown>
                </Box>
              </ContentCard>
            );
          }}
        </TypewriterText>
      </Box>
    </Box>
  );
});

ResponseBlock.displayName = 'ResponseBlock';

// --- StreamingToolContent ---
// Auto-scrolling markdown block for streamed tool result content (e.g. session search summary).

const StreamingToolContent = memo(({ content }: { content: string }) => {
  const scrollRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [content]);

  return (
    <ContentCard ref={scrollRef} maxHeight="150px">
      <Box sx={{
        fontSize: '0.85rem',
        '& p': { my: 0.5, lineHeight: 1.6 },
        '& ul, & ol': { pl: 2.5, my: 0.5 },
        '& li': { my: 0.25 },
        '& h1, & h2, & h3, & h4': { mt: 1, mb: 0.5, fontSize: '0.9rem', fontWeight: 600 },
      }}>
        <ReactMarkdown
          components={thoughtMarkdownComponents}
          remarkPlugins={remarkPlugins}
          skipHtml
        >
          {content}
        </ReactMarkdown>
      </Box>
    </ContentCard>
  );
});

StreamingToolContent.displayName = 'StreamingToolContent';

// --- StreamingContentRenderer ---

/**
 * StreamingContentRenderer Component
 * 
 * Renders streaming LLM content with typewriter effect.
 * Routes to appropriate visual treatment based on event_type.
 */
const StreamingContentRenderer = memo(({ item, stageType }: StreamingContentRendererProps) => {
  // Thinking (llm_thinking) — italic, secondary color
  // All thought types use the same visual treatment (matching ThinkingItem).
  // Renders immediately (showing the "Thinking..." label) even before content
  // arrives — ThinkingBlock internally defers the gray content box until the
  // typewriter produces visible text.
  if (item.eventType === TIMELINE_EVENT_TYPES.LLM_THINKING) {
    return <ThinkingBlock content={item.content || ''} />;
  }

  if (item.eventType === TIMELINE_EVENT_TYPES.LLM_RESPONSE) {
    if (!item.content || !item.content.trim()) return null;
    return <ResponseBlock content={item.content} />;
  }
  
  if (item.eventType === TIMELINE_EVENT_TYPES.MCP_TOOL_SUMMARY) {
    const isPlaceholder = item.content === 'Summarizing tool results...';
    
    return (
      <Box sx={{ mb: 1.5 }}>
        <Box sx={{ display: 'flex', gap: 1.5, mb: 0.5 }}>
          <Typography variant="body2" sx={{ fontSize: '1.1rem', lineHeight: 1, flexShrink: 0 }}>
            📋
          </Typography>
          <Typography
            variant="caption"
            sx={{
              fontWeight: 700, textTransform: 'uppercase', letterSpacing: 0.5,
              fontSize: '0.75rem', color: 'warning.main', mt: 0.25
            }}
          >
            TOOL RESULT SUMMARY
          </Typography>
        </Box>
        <Box sx={(theme) => ({ pl: 3.5, ml: 3.5, py: 0.5, borderLeft: `2px solid ${alpha(theme.palette.warning.main, 0.2)}` })}>
          {isPlaceholder ? (
            <Typography
              variant="body1"
              sx={{
                whiteSpace: 'pre-wrap', wordBreak: 'break-word', lineHeight: 1.7,
                fontSize: '1rem', color: 'text.disabled', fontStyle: 'italic',
                animation: 'pulse 1.5s ease-in-out infinite',
                '@keyframes pulse': { '0%, 100%': { opacity: 0.3 }, '50%': { opacity: 1 } }
              }}
            >
              {item.content}
            </Typography>
          ) : (
            <TypewriterText text={item.content}>
              {(displayText) => (
                <Box sx={{ color: 'text.secondary' }}>
                  <ReactMarkdown components={thoughtMarkdownComponents} remarkPlugins={remarkPlugins} skipHtml>
                    {displayText}
                  </ReactMarkdown>
                </Box>
              )}
            </TypewriterText>
          )}
        </Box>
      </Box>
    );
  }
  
  if (item.eventType === TIMELINE_EVENT_TYPES.FINAL_ANALYSIS) {
    const isReflector = item.metadata?.interaction_type === LLM_INTERACTION_TYPE.MEMORY_EXTRACTION;
    const { label, emoji, color } = isReflector
      ? { label: 'LESSONS LEARNED', emoji: '🧠', color: 'secondary.main' }
      : getFinalAnalysisPresentation(item.metadata, stageType, !!item.metadata?.forced_conclusion);
    return (
      <Box sx={{ mb: 2, mt: 3 }}>
        <Box sx={{ display: 'flex', gap: 1.5, mb: 0.5 }}>
          <Typography variant="body2" sx={{ fontSize: '1.1rem', lineHeight: 1, flexShrink: 0 }}>
            {emoji}
          </Typography>
          <Typography
            variant="caption"
            sx={{
              fontWeight: 700, textTransform: 'uppercase', letterSpacing: 0.5,
              fontSize: '0.75rem', color, mt: 0.25
            }}
          >
            {label}
          </Typography>
        </Box>
        <Box sx={{ flex: 1, minWidth: 0, ml: 4, color: 'text.primary' }}>
          <TypewriterText text={item.content}>
            {(displayText) => (
              <ReactMarkdown components={thoughtMarkdownComponents} remarkPlugins={remarkPlugins} skipHtml>
                {displayText}
              </ReactMarkdown>
            )}
          </TypewriterText>
        </Box>
      </Box>
    );
  }

  // In-progress tool call
  if (item.eventType === TIMELINE_EVENT_TYPES.LLM_TOOL_CALL) {
    const toolName = (item.metadata?.tool_name as string) || 'unknown';
    const toolType = (item.metadata?.tool_type as string);
    const isSkill = toolType === TOOL_TYPE.SKILL;
    const isMemory = toolType === TOOL_TYPE.MEMORY;
    const { accentKey: paletteKey } = getToolVisualConfig(toolType, { mode: 'streaming' });

    let displayName = toolName;
    let statusLabel = 'Executing...';
    if (isMemory) {
      const isSessionSearch = toolName === MEMORY_TOOL_NAME.SEARCH_PAST_SESSIONS;
      displayName = isSessionSearch ? 'Searching Past Sessions' : 'Recalling Insights';
      const query = (() => {
        const raw = item.metadata?.arguments;
        if (!raw) return null;
        if (typeof raw === 'object' && !Array.isArray(raw)) return (raw as Record<string, unknown>).query as string | undefined;
        if (typeof raw === 'string') { try { return (JSON.parse(raw) as Record<string, unknown>).query as string | undefined; } catch { return null; } }
        return null;
      })();
      statusLabel = query ? String(query) : 'Searching...';
    } else if (isSkill) {
      displayName = 'Loading Skills';
      statusLabel = getSkillNamesLabel(item.metadata?.arguments) ?? 'Loading...';
    }

    const hasStreamedContent = isMemory && item.content && item.content.trim().length > 0;

    return (
      <Box sx={{ ml: 4, my: 1, mr: 1 }}>
        <Box
          sx={(theme) => ({
            border: '1px solid',
            borderColor: alpha(theme.palette[paletteKey].main, 0.25),
            borderRadius: 1.5,
            bgcolor: alpha(theme.palette[paletteKey].main, 0.04),
          })}
        >
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: 1.5,
              px: 1.5,
              py: 0.75,
            }}
          >
            <Box
              sx={(theme) => ({
                width: 18,
                height: 18,
                border: '2px solid',
                borderColor: theme.palette[paletteKey].main,
                borderTopColor: 'transparent',
                borderRadius: '50%',
                flexShrink: 0,
                animation: 'spin 1s linear infinite',
                '@keyframes spin': {
                  '0%': { transform: 'rotate(0deg)' },
                  '100%': { transform: 'rotate(360deg)' },
                },
              })}
            />
            <Typography
              variant="body2"
              sx={(theme) => ({
                fontFamily: 'monospace',
                fontWeight: 600,
                fontSize: '0.9rem',
                color: theme.palette[paletteKey].main,
              })}
            >
              {displayName}
            </Typography>
            <Typography variant="caption" color="text.secondary" sx={{ fontSize: '0.8rem', flex: 1 }}>
              {statusLabel}
            </Typography>
          </Box>
          {hasStreamedContent && (
            <StreamingToolContent content={item.content} />
          )}
        </Box>
      </Box>
    );
  }

  if (item.eventType === TIMELINE_EVENT_TYPES.EXECUTIVE_SUMMARY) {
    if (!item.content || !item.content.trim()) return null;
    return (
      <Box sx={{ mb: 1.5, display: 'flex', gap: 1.5 }}>
        <Typography variant="body2" sx={{ fontSize: '1.1rem', lineHeight: 1, flexShrink: 0, mt: 0.25 }}>
          ✨
        </Typography>
        <TypewriterText text={item.content}>
          {(displayText) => (
            <Box sx={{ flex: 1, minWidth: 0, color: 'text.primary' }}>
              <ReactMarkdown components={thoughtMarkdownComponents} remarkPlugins={remarkPlugins} skipHtml>
                {displayText}
              </ReactMarkdown>
            </Box>
          )}
        </TypewriterText>
      </Box>
    );
  }
  
  return null;
});

StreamingContentRenderer.displayName = 'StreamingContentRenderer';

export default StreamingContentRenderer;
