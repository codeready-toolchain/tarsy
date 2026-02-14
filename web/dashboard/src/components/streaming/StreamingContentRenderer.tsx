import { memo, useEffect, useRef } from 'react';
import { Box, Typography, alpha } from '@mui/material';
import ReactMarkdown, { defaultUrlTransform } from 'react-markdown';
import TypewriterText from './TypewriterText';
import { 
  hasMarkdownSyntax, 
  thoughtMarkdownComponents 
} from '../../utils/markdownComponents';
import { TIMELINE_EVENT_TYPES } from '../../constants/eventTypes';

/**
 * StreamingItem for the streaming content renderer.
 * Maps to a streaming timeline event with type and content.
 */
export interface StreamingItem {
  eventType: string;
  content: string;
}

interface StreamingContentRendererProps {
  item: StreamingItem;
}

// --- ThinkingBlock ---

interface ThinkingBlockProps {
  content: string;
  textColor: string;
  isItalic?: boolean;
}

const ThinkingBlock = memo(({ content, textColor, isItalic = false }: ThinkingBlockProps) => {
  const hasMarkdown = hasMarkdownSyntax(content);
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
        <Box 
          ref={scrollContainerRef}
          sx={(theme) => ({ 
            bgcolor: alpha(theme.palette.grey[300], 0.15),
            border: '1px solid',
            borderColor: alpha(theme.palette.grey[400], 0.2),
            borderRadius: 1, p: 1.5,
            height: '150px', overflowY: 'auto',
            '&::-webkit-scrollbar': { width: '8px' },
            '&::-webkit-scrollbar-track': { bgcolor: 'transparent' },
            '&::-webkit-scrollbar-thumb': {
              bgcolor: alpha(theme.palette.grey[500], 0.3), borderRadius: '4px',
              '&:hover': { bgcolor: alpha(theme.palette.grey[500], 0.5) }
            }
          })}
        >
          <TypewriterText text={content} speed={3}>
            {(displayText) => (
              hasMarkdown ? (
                <Box sx={isItalic ? { '& p, & li': { color: textColor, fontStyle: 'italic' } } : undefined}>
                  <ReactMarkdown components={thoughtMarkdownComponents} skipHtml>
                    {displayText}
                  </ReactMarkdown>
                </Box>
              ) : (
                <Typography 
                  variant="body1" 
                  sx={{ 
                    whiteSpace: 'pre-wrap', wordBreak: 'break-word',
                    lineHeight: 1.7, fontSize: '1rem', color: textColor,
                    fontStyle: isItalic ? 'italic' : 'normal'
                  }}
                >
                  {displayText}
                </Typography>
              )
            )}
          </TypewriterText>
        </Box>
      </Box>
    </Box>
  );
});

ThinkingBlock.displayName = 'ThinkingBlock';

// --- StreamingContentRenderer ---

/**
 * StreamingContentRenderer Component
 * 
 * Renders streaming LLM content with typewriter effect.
 * Routes to appropriate visual treatment based on event_type.
 */
const StreamingContentRenderer = memo(({ item }: StreamingContentRendererProps) => {
  // Thinking (llm_thinking)
  if (item.eventType === TIMELINE_EVENT_TYPES.LLM_THINKING) {
    return <ThinkingBlock content={item.content} textColor="text.primary" />;
  }

  // Native thinking (native_thinking) — italic, secondary color
  if (item.eventType === TIMELINE_EVENT_TYPES.NATIVE_THINKING) {
    return <ThinkingBlock content={item.content} textColor="text.secondary" isItalic />;
  }

  // Response (llm_response) — intermediate iterations
  if (item.eventType === TIMELINE_EVENT_TYPES.LLM_RESPONSE) {
    const hasMarkdown = hasMarkdownSyntax(item.content);
    return (
      <Box sx={{ mb: 1.5, display: 'flex', gap: 1.5 }}>
        <Typography variant="body2" sx={{ fontSize: '1.1rem', lineHeight: 1, flexShrink: 0, mt: 0.25 }}>
          💬
        </Typography>
        <TypewriterText text={item.content} speed={3}>
          {(displayText) => (
            <Box sx={{ flex: 1, minWidth: 0 }}>
              {hasMarkdown ? (
                <ReactMarkdown components={thoughtMarkdownComponents} skipHtml>
                  {displayText}
                </ReactMarkdown>
              ) : (
                <Typography 
                  variant="body1" 
                  sx={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word', lineHeight: 1.7, fontSize: '1rem', color: 'text.primary' }}
                >
                  {displayText}
                </Typography>
              )}
            </Box>
          )}
        </TypewriterText>
      </Box>
    );
  }
  
  // Tool result summary (mcp_tool_summary)
  if (item.eventType === TIMELINE_EVENT_TYPES.MCP_TOOL_SUMMARY) {
    const isPlaceholder = item.content === 'Summarizing tool results...';
    const hasMarkdown = !isPlaceholder && hasMarkdownSyntax(item.content);
    
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
              fontSize: '0.75rem', color: 'rgba(237, 108, 2, 0.9)', mt: 0.25
            }}
          >
            TOOL RESULT SUMMARY
          </Typography>
        </Box>
        <Box sx={{ pl: 3.5, ml: 3.5, py: 0.5, borderLeft: '2px solid rgba(237, 108, 2, 0.2)' }}>
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
            <TypewriterText text={item.content} speed={3}>
              {(displayText) => (
                hasMarkdown ? (
                  <Box sx={{ '& p': { color: 'text.secondary' }, '& li': { color: 'text.secondary' } }}>
                    <ReactMarkdown components={thoughtMarkdownComponents} skipHtml>
                      {displayText}
                    </ReactMarkdown>
                  </Box>
                ) : (
                  <Typography
                    variant="body1"
                    sx={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word', lineHeight: 1.7, fontSize: '1rem', color: 'text.secondary' }}
                  >
                    {displayText}
                  </Typography>
                )
              )}
            </TypewriterText>
          )}
        </Box>
      </Box>
    );
  }
  
  // Final analysis
  if (item.eventType === TIMELINE_EVENT_TYPES.FINAL_ANALYSIS) {
    const hasMarkdown = hasMarkdownSyntax(item.content);
    return (
      <Box sx={{ mb: 2, mt: 3 }}>
        <Box sx={{ display: 'flex', gap: 1.5, mb: 0.5 }}>
          <Typography variant="body2" sx={{ fontSize: '1.1rem', lineHeight: 1, flexShrink: 0 }}>
            🎯
          </Typography>
          <Typography
            variant="caption"
            sx={{
              fontWeight: 700, textTransform: 'uppercase', letterSpacing: 0.5,
              fontSize: '0.75rem', color: '#2e7d32', mt: 0.25
            }}
          >
            FINAL ANSWER
          </Typography>
        </Box>
        <Box sx={{ flex: 1, minWidth: 0, ml: 4 }}>
          <TypewriterText text={item.content} speed={3}>
            {(displayText) => (
              hasMarkdown ? (
                <Box sx={{ color: 'text.primary' }}>
                  <ReactMarkdown urlTransform={defaultUrlTransform} components={thoughtMarkdownComponents}>
                    {displayText}
                  </ReactMarkdown>
                </Box>
              ) : (
                <Typography 
                  variant="body1" 
                  sx={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word', lineHeight: 1.7, fontSize: '1rem', color: 'text.primary' }}
                >
                  {displayText}
                </Typography>
              )
            )}
          </TypewriterText>
        </Box>
      </Box>
    );
  }

  // Executive summary
  if (item.eventType === TIMELINE_EVENT_TYPES.EXECUTIVE_SUMMARY) {
    const hasMarkdown = hasMarkdownSyntax(item.content);
    return (
      <Box sx={{ mb: 1.5, display: 'flex', gap: 1.5 }}>
        <Typography variant="body2" sx={{ fontSize: '1.1rem', lineHeight: 1, flexShrink: 0, mt: 0.25 }}>
          ✨
        </Typography>
        <TypewriterText text={item.content} speed={3}>
          {(displayText) => (
            <Box sx={{ flex: 1, minWidth: 0 }}>
              {hasMarkdown ? (
                <ReactMarkdown components={thoughtMarkdownComponents} skipHtml>
                  {displayText}
                </ReactMarkdown>
              ) : (
                <Typography 
                  variant="body1" 
                  sx={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word', lineHeight: 1.7, fontSize: '1rem', color: 'text.primary' }}
                >
                  {displayText}
                </Typography>
              )}
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
