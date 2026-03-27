import { useState, useMemo, memo } from 'react';
import { Box, Typography, Collapse, IconButton, Chip, alpha, useTheme } from '@mui/material';
import { useColorScheme } from '@mui/material/styles';
import { ExpandMore, ExpandLess, Code, Search, Link as LinkIcon } from '@mui/icons-material';
import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter';
import { vs, vscDarkPlus } from 'react-syntax-highlighter/dist/esm/styles/prism';
import { FLOW_ITEM, type FlowItem } from '../../utils/timelineParser';
import { highlightSearchTermNodes } from '../../utils/search';

interface CodeBlock {
  type: string;
  content: string;
  language?: string;
  outcome?: string;
  exit_code?: number;
}

interface NativeToolItemProps {
  item: FlowItem;
  searchTerm?: string;
}

/** Parse content preview summary for the header */
function getPreviewSummary(item: FlowItem): string {
  try {
    const parsed = JSON.parse(item.content);
    if (item.type === FLOW_ITEM.CODE_EXECUTION) {
      const blocks: string[] = [];
      if (Array.isArray(parsed.blocks)) {
        const codeCount = parsed.blocks.filter((b: CodeBlock) => b.type === 'code').length;
        const outputCount = parsed.blocks.filter((b: CodeBlock) => b.type === 'output').length;
        if (codeCount > 0) blocks.push(`${codeCount} code block${codeCount > 1 ? 's' : ''}`);
        if (outputCount > 0) blocks.push(`${outputCount} output${outputCount > 1 ? 's' : ''}`);
        return blocks.join(', ') || '1 code block';
      }
      if (parsed.code) return '1 code block';
    }
    if (item.type === FLOW_ITEM.SEARCH_RESULT && Array.isArray(parsed.queries)) {
      return `${parsed.queries.length} quer${parsed.queries.length === 1 ? 'y' : 'ies'}`;
    }
    if (item.type === FLOW_ITEM.URL_CONTEXT && Array.isArray(parsed.urls)) {
      return `${parsed.urls.length} URL${parsed.urls.length === 1 ? '' : 's'}`;
    }
  } catch {
    // Not JSON
  }
  return '';
}

const PRE_OVERRIDES = {
  '& pre': {
    margin: '0 !important',
    padding: '12px !important',
    fontSize: '0.875rem !important',
    lineHeight: '1.5 !important',
    backgroundColor: 'transparent !important',
  },
};

const OUTPUT_FONT = 'Consolas, Monaco, "Courier New", monospace';

/**
 * NativeToolItem - renders code_execution, search_result, and url_context timeline events.
 * Uses info/teal color scheme to differentiate from MCP tool calls.
 */
function NativeToolItem({ item, searchTerm }: NativeToolItemProps) {
  const [expanded, setExpanded] = useState(false);
  const theme = useTheme();
  const { mode, systemMode } = useColorScheme();
  const isDark = mode === 'dark' || (mode === 'system' && systemMode === 'dark');
  const boxColor = theme.palette.info.main;

  const previewSummary = useMemo(() => getPreviewSummary(item), [item]);

  const getIcon = () => {
    switch (item.type) {
      case 'code_execution':
        return <Code sx={{ fontSize: 18, color: boxColor }} />;
      case 'search_result':
        return <Search sx={{ fontSize: 18, color: boxColor }} />;
      case 'url_context':
        return <LinkIcon sx={{ fontSize: 18, color: boxColor }} />;
      default:
        return <Code sx={{ fontSize: 18, color: boxColor }} />;
    }
  };

  const getTitle = () => {
    switch (item.type) {
      case 'code_execution':
        return 'Code Execution';
      case 'search_result':
        return 'Google Search';
      case 'url_context':
        return 'URL Context';
      default:
        return 'Native Tool';
    }
  };

  const renderContent = () => {
    if (item.type === FLOW_ITEM.CODE_EXECUTION) {
      // Try to parse code execution content
      try {
        const parsed = JSON.parse(item.content);

        // Multi-block format
        if (Array.isArray(parsed.blocks)) {
          let codeIndex = 0;
          let outputIndex = 0;
          return (
            <Box>
              {parsed.blocks.map((block: CodeBlock, idx: number) => {
                if (block.type === 'code') {
                  codeIndex++;
                  const lang = block.language || 'python';
                  const label =
                    parsed.blocks.filter((b: CodeBlock) => b.type === 'code').length > 1
                      ? `Generated Code ${codeIndex} (${lang})`
                      : `Generated Code (${lang})`;
                  return (
                    <Box key={idx} sx={{ mb: 1.5 }}>
                      <Typography
                        variant="caption"
                        sx={{
                          fontWeight: 600,
                          fontSize: '0.75rem',
                          color: 'text.secondary',
                          mb: 0.5,
                          display: 'block',
                        }}
                      >
                        {label}
                      </Typography>
                      <Box
                        sx={{
                          bgcolor: theme.palette.action.hover,
                          borderRadius: 1,
                          border: `1px solid ${theme.palette.divider}`,
                          overflow: 'auto',
                          maxHeight: 400,
                          ...PRE_OVERRIDES,
                        }}
                      >
                        <SyntaxHighlighter
                          language={lang}
                          style={isDark ? vscDarkPlus : vs}
                          customStyle={{
                            margin: 0,
                            padding: '12px',
                            fontSize: '0.875rem',
                            lineHeight: 1.5,
                            backgroundColor: 'transparent',
                          }}
                          wrapLines
                          wrapLongLines
                        >
                          {block.content}
                        </SyntaxHighlighter>
                      </Box>
                    </Box>
                  );
                }
                if (block.type === 'output') {
                  outputIndex++;
                  const outputLabel =
                    parsed.blocks.filter((b: CodeBlock) => b.type === 'output').length > 1
                      ? `Execution Output ${outputIndex}`
                      : 'Output';
                  const isOk = block.outcome === 'ok' || block.exit_code === 0;
                  return (
                    <Box key={idx}>
                      <Box
                        sx={{
                          display: 'flex',
                          alignItems: 'center',
                          gap: 1,
                          mb: 0.5,
                        }}
                      >
                        <Typography
                          variant="caption"
                          sx={{
                            fontWeight: 600,
                            fontSize: '0.75rem',
                            color: 'text.secondary',
                            display: 'block',
                          }}
                        >
                          {outputLabel}
                        </Typography>
                        {block.outcome != null && (
                          <Chip
                            label={isOk ? 'ok' : 'error'}
                            size="small"
                            color={isOk ? 'success' : 'error'}
                            variant="outlined"
                            sx={{ height: 18, fontSize: '0.65rem' }}
                          />
                        )}
                      </Box>
                      <Box
                        sx={{
                          bgcolor: theme.palette.action.hover,
                          borderRadius: 1,
                          border: `1px solid ${theme.palette.divider}`,
                          p: 1.5,
                          overflow: 'auto',
                          maxHeight: 300,
                        }}
                      >
                        <pre
                          style={{
                            margin: 0,
                            fontFamily: OUTPUT_FONT,
                            fontSize: '0.875rem',
                            whiteSpace: 'pre-wrap',
                            wordBreak: 'break-word',
                          }}
                        >
                          {highlightSearchTermNodes(block.content, searchTerm ?? '')}
                        </pre>
                      </Box>
                    </Box>
                  );
                }
                return null;
              })}
            </Box>
          );
        }

        // Single code/output format
        return (
          <Box>
            {parsed.code && (
              <Box sx={{ mb: 1.5 }}>
                <Typography
                  variant="caption"
                  sx={{
                    fontWeight: 600,
                    fontSize: '0.75rem',
                    color: 'text.secondary',
                    mb: 0.5,
                    display: 'block',
                  }}
                >
                  Generated Code ({parsed.language || 'python'})
                </Typography>
                <Box
                  sx={{
                    bgcolor: theme.palette.action.hover,
                    borderRadius: 1,
                    border: `1px solid ${theme.palette.divider}`,
                    overflow: 'auto',
                    maxHeight: 400,
                    ...PRE_OVERRIDES,
                  }}
                >
                  <SyntaxHighlighter
                    language={parsed.language || 'python'}
                    style={isDark ? vscDarkPlus : vs}
                    customStyle={{
                      margin: 0,
                      padding: '12px',
                      fontSize: '0.875rem',
                      lineHeight: 1.5,
                      backgroundColor: 'transparent',
                    }}
                    wrapLines
                    wrapLongLines
                  >
                    {parsed.code}
                  </SyntaxHighlighter>
                </Box>
              </Box>
            )}
            {parsed.output && (
              <Box>
                <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 0.5 }}>
                  <Typography
                    variant="caption"
                    sx={{
                      fontWeight: 600,
                      fontSize: '0.75rem',
                      color: 'text.secondary',
                      display: 'block',
                    }}
                  >
                    Output
                  </Typography>
                  {parsed.outcome != null && (
                    <Chip
                      label={parsed.outcome === 'ok' || parsed.exit_code === 0 ? 'ok' : 'error'}
                      size="small"
                      color={
                        parsed.outcome === 'ok' || parsed.exit_code === 0 ? 'success' : 'error'
                      }
                      variant="outlined"
                      sx={{ height: 18, fontSize: '0.65rem' }}
                    />
                  )}
                </Box>
                <Box
                  sx={{
                    bgcolor: theme.palette.action.hover,
                    borderRadius: 1,
                    border: `1px solid ${theme.palette.divider}`,
                    p: 1.5,
                    overflow: 'auto',
                    maxHeight: 300,
                  }}
                >
                  <pre
                    style={{
                      margin: 0,
                      fontFamily: OUTPUT_FONT,
                      fontSize: '0.875rem',
                      whiteSpace: 'pre-wrap',
                      wordBreak: 'break-word',
                    }}
                  >
                    {highlightSearchTermNodes(parsed.output, searchTerm ?? '')}
                  </pre>
                </Box>
              </Box>
            )}
          </Box>
        );
      } catch {
        // Not JSON, render as raw code
        return (
          <Box
            sx={{
              bgcolor: theme.palette.action.hover,
              borderRadius: 1,
              border: `1px solid ${theme.palette.divider}`,
              overflow: 'auto',
              maxHeight: 400,
              ...PRE_OVERRIDES,
            }}
          >
            <SyntaxHighlighter
              language="python"
              style={isDark ? vscDarkPlus : vs}
              customStyle={{
                margin: 0,
                padding: '12px',
                fontSize: '0.875rem',
                lineHeight: 1.5,
                backgroundColor: 'transparent',
              }}
              wrapLines
              wrapLongLines
            >
              {item.content}
            </SyntaxHighlighter>
          </Box>
        );
      }
    }

    if (item.type === FLOW_ITEM.SEARCH_RESULT) {
      try {
        const parsed = JSON.parse(item.content);
        if (Array.isArray(parsed.queries)) {
          return (
            <Box
              sx={{
                bgcolor: theme.palette.action.hover,
                borderRadius: 1,
                border: `1px solid ${theme.palette.divider}`,
                p: 1.5,
              }}
            >
              {parsed.queries.map((query: string, idx: number) => (
                <Typography
                  key={idx}
                  variant="body2"
                  sx={{
                    fontFamily: OUTPUT_FONT,
                    fontSize: '0.85rem',
                    mb: idx < parsed.queries.length - 1 ? 0.75 : 0,
                    color: 'text.primary',
                  }}
                >
                  {idx + 1}. &quot;{highlightSearchTermNodes(query, searchTerm ?? '')}&quot;
                </Typography>
              ))}
            </Box>
          );
        }
      } catch {
        /* fall through */
      }
      return (
        <Typography
          variant="body2"
          sx={{
            fontFamily: OUTPUT_FONT,
            fontSize: '0.85rem',
            whiteSpace: 'pre-wrap',
          }}
        >
          {highlightSearchTermNodes(item.content, searchTerm ?? '')}
        </Typography>
      );
    }

    if (item.type === FLOW_ITEM.URL_CONTEXT) {
      try {
        const parsed = JSON.parse(item.content);
        if (Array.isArray(parsed.urls)) {
          return (
            <Box
              sx={{
                bgcolor: theme.palette.action.hover,
                borderRadius: 1,
                border: `1px solid ${theme.palette.divider}`,
                p: 1.5,
              }}
            >
              {parsed.urls.map(
                (url: { title?: string; uri: string }, idx: number) => (
                  <Box key={idx} sx={{ mb: idx < parsed.urls.length - 1 ? 1 : 0 }}>
                    <Typography
                      variant="body2"
                      sx={{
                        fontWeight: 600,
                        fontSize: '0.85rem',
                        mb: 0.25,
                        color: 'text.primary',
                      }}
                    >
                      {highlightSearchTermNodes(url.title || 'Untitled', searchTerm ?? '')}
                    </Typography>
                    <Typography
                      variant="caption"
                      sx={{
                        fontFamily: OUTPUT_FONT,
                        fontSize: '0.75rem',
                        color: 'text.secondary',
                        wordBreak: 'break-all',
                      }}
                    >
                      {highlightSearchTermNodes(url.uri, searchTerm ?? '')}
                    </Typography>
                  </Box>
                ),
              )}
            </Box>
          );
        }
      } catch {
        /* fall through */
      }
      return (
        <Typography
          variant="body2"
          sx={{
            fontFamily: OUTPUT_FONT,
            fontSize: '0.85rem',
            whiteSpace: 'pre-wrap',
          }}
        >
          {highlightSearchTermNodes(item.content, searchTerm ?? '')}
        </Typography>
      );
    }

    return (
      <Typography variant="body2" sx={{ whiteSpace: 'pre-wrap' }}>
        {highlightSearchTermNodes(item.content, searchTerm ?? '')}
      </Typography>
    );
  };

  return (
    <Box
      data-flow-item-id={item.id}
      sx={{
        ml: 4,
        my: 0.5,
        mr: 1,
        border: '1px solid',
        borderColor: alpha(boxColor, 0.25),
        borderRadius: 1.5,
        bgcolor: alpha(boxColor, 0.04),
      }}
    >
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: 1,
          px: 1.5,
          py: 0.75,
          cursor: 'pointer',
          borderRadius: 1.5,
          transition: 'background-color 0.2s ease',
          '&:hover': { bgcolor: alpha(boxColor, 0.1) },
        }}
        onClick={() => setExpanded(!expanded)}
      >
        {getIcon()}
        <Typography
          variant="body2"
          sx={{
          fontFamily: 'monospace',
          fontWeight: 500,
          fontSize: '0.9rem',
          color: 'text.secondary',
          }}
        >
          {getTitle()}
        </Typography>
        {previewSummary && (
          <Typography
            variant="caption"
            color="text.secondary"
            sx={{ fontSize: '0.8rem', flex: 1, lineHeight: 1.4 }}
          >
            {previewSummary}
          </Typography>
        )}
        {!previewSummary && (
          <Typography
            variant="caption"
            color="text.secondary"
            sx={{ fontSize: '0.8rem', flex: 1, lineHeight: 1.4 }}
          />
        )}
        <IconButton size="small" sx={{ p: 0.25 }}>
          {expanded ? <ExpandLess fontSize="small" /> : <ExpandMore fontSize="small" />}
        </IconButton>
      </Box>

      <Collapse in={expanded}>
        <Box sx={{ px: 1.5, pb: 1.5, pt: 0.5, borderTop: 1, borderColor: 'divider' }}>
          {renderContent()}
        </Box>
      </Collapse>
    </Box>
  );
}

export default memo(NativeToolItem);
