/**
 * NativeToolsDisplay — shows native tool config and usage for an LLM interaction.
 *
 * Reads enabled tools from `llm_request.native_tools` and infers usage from
 * `llm_response` (code_executions, groundings_count) and `response_metadata`
 * (grounding type details).
 *
 * Visual pattern from old dashboard's NativeToolsDisplay.tsx,
 * data layer rewritten for the new trace detail response.
 */

import { memo, useMemo } from 'react';
import {
  Box,
  Chip,
  Typography,
  Stack,
  Accordion,
  AccordionSummary,
  AccordionDetails,
} from '@mui/material';
import ExpandMoreIcon from '@mui/icons-material/ExpandMore';
import SearchIcon from '@mui/icons-material/Search';
import CodeIcon from '@mui/icons-material/Code';
import LinkIcon from '@mui/icons-material/Link';

import type { LLMInteractionDetailResponse } from '../../types/trace';

/* ── Tool key constants ──────────────────────────────────────────────── */

const TOOL_KEYS = {
  GOOGLE_SEARCH: 'google_search',
  CODE_EXECUTION: 'code_execution',
  URL_CONTEXT: 'url_context',
} as const;

type ToolKey = (typeof TOOL_KEYS)[keyof typeof TOOL_KEYS];

const KNOWN_TOOL_KEYS: ReadonlySet<string> = new Set<string>(Object.values(TOOL_KEYS));

/** Type guard: returns true (and narrows to ToolKey) only for recognised tool keys. */
function isKnownToolKey(key: string): key is ToolKey {
  return KNOWN_TOOL_KEYS.has(key);
}

/* ── Tool metadata helpers ───────────────────────────────────────────── */

function getToolDisplayName(key: ToolKey): string {
  switch (key) {
    case TOOL_KEYS.GOOGLE_SEARCH:
      return 'Google Search';
    case TOOL_KEYS.CODE_EXECUTION:
      return 'Code Execution';
    case TOOL_KEYS.URL_CONTEXT:
      return 'URL Context';
  }
}

function getToolIcon(key: ToolKey) {
  switch (key) {
    case TOOL_KEYS.GOOGLE_SEARCH:
      return SearchIcon;
    case TOOL_KEYS.CODE_EXECUTION:
      return CodeIcon;
    case TOOL_KEYS.URL_CONTEXT:
      return LinkIcon;
  }
}

function getToolChipColor(key: ToolKey): 'primary' | 'secondary' | 'info' {
  switch (key) {
    case TOOL_KEYS.GOOGLE_SEARCH:
      return 'primary';
    case TOOL_KEYS.CODE_EXECUTION:
      return 'secondary';
    case TOOL_KEYS.URL_CONTEXT:
      return 'info';
  }
}

/* ── Usage inference ─────────────────────────────────────────────────── */

interface ToolUsageInfo {
  google_search?: { queries: string[]; sourcesCount: number };
  code_execution?: { count: number };
  url_context?: { urls: { uri: string; title: string }[] };
}

function inferToolUsage(detail: LLMInteractionDetailResponse): ToolUsageInfo {
  const usage: ToolUsageInfo = {};

  const llmResp = detail.llm_response as Record<string, unknown> | undefined;
  const respMeta = detail.response_metadata as Record<string, unknown> | undefined;

  // Code execution: present in llm_response.code_executions
  if (llmResp) {
    const codeExecs = llmResp['code_executions'];
    if (Array.isArray(codeExecs) && codeExecs.length > 0) {
      usage.code_execution = { count: codeExecs.length };
    }
  }

  // Groundings: detailed info in response_metadata.groundings
  if (respMeta) {
    const groundings = respMeta['groundings'];
    if (Array.isArray(groundings)) {
      const searchQueries: string[] = [];
      let searchSourcesCount = 0;
      const urlContextUrls: { uri: string; title: string }[] = [];

      for (const g of groundings) {
        const gObj = g as Record<string, unknown>;
        const gType = gObj['type'] as string;

        if (gType === 'google_search') {
          const queries = gObj['queries'];
          if (Array.isArray(queries)) {
            searchQueries.push(...(queries as string[]));
          }
          const sources = gObj['sources'];
          if (Array.isArray(sources)) {
            searchSourcesCount += sources.length;
          }
        } else if (gType === 'url_context') {
          const sources = gObj['sources'];
          if (Array.isArray(sources)) {
            for (const s of sources) {
              const src = s as Record<string, string>;
              urlContextUrls.push({ uri: src['uri'] || '', title: src['title'] || '' });
            }
          }
        }
      }

      if (searchQueries.length > 0) {
        usage.google_search = { queries: searchQueries, sourcesCount: searchSourcesCount };
      }
      if (urlContextUrls.length > 0) {
        usage.url_context = { urls: urlContextUrls };
      }
    }
  }

  return usage;
}

/* ── Component ───────────────────────────────────────────────────────── */

interface NativeToolsDisplayProps {
  detail: LLMInteractionDetailResponse;
  variant: 'compact' | 'detailed';
}

function NativeToolsDisplay({ detail, variant }: NativeToolsDisplayProps) {
  const nativeToolsConfig = detail.llm_request?.['native_tools'] as
    | Record<string, boolean>
    | undefined;

  const toolUsage = useMemo(() => inferToolUsage(detail), [detail]);

  // Collect tools to display: enabled or used
  const tools = useMemo(() => {
    const toolSet = new Set<ToolKey>();

    if (nativeToolsConfig) {
      for (const [key, enabled] of Object.entries(nativeToolsConfig)) {
        if (enabled && isKnownToolKey(key)) toolSet.add(key);
      }
    }

    // Also add tools that were actually used (even if config is missing)
    if (toolUsage.google_search) toolSet.add(TOOL_KEYS.GOOGLE_SEARCH);
    if (toolUsage.code_execution) toolSet.add(TOOL_KEYS.CODE_EXECUTION);
    if (toolUsage.url_context) toolSet.add(TOOL_KEYS.URL_CONTEXT);

    return Array.from(toolSet);
  }, [nativeToolsConfig, toolUsage]);

  if (tools.length === 0) return null;

  if (variant === 'compact') {
    return <CompactView tools={tools} toolUsage={toolUsage} />;
  }

  return <DetailedView tools={tools} toolUsage={toolUsage} />;
}

/* ── Compact (for LLMInteractionPreview area in LLMInteractionDetail) ── */

function CompactView({ tools, toolUsage }: { tools: ToolKey[]; toolUsage: ToolUsageInfo }) {
  return (
    <Box sx={{ display: 'flex', gap: 0.5, flexWrap: 'wrap', alignItems: 'center' }}>
      {tools.map((key) => {
        const Icon = getToolIcon(key);
        const used = isUsed(toolUsage, key);
        const displayName = getToolDisplayName(key);

        return (
          <Chip
            key={key}
            icon={<Icon sx={{ fontSize: '0.875rem' }} />}
            label={displayName}
            size="small"
            color={getToolChipColor(key)}
            variant={used ? 'filled' : 'outlined'}
            sx={{
              fontSize: '0.7rem',
              height: '20px',
              '& .MuiChip-label': { px: 0.75 },
              '& .MuiChip-icon': { ml: 0.5 },
            }}
          />
        );
      })}
    </Box>
  );
}

/* ── Detailed (for expanded LLMInteractionDetail) ────────────────────── */

function DetailedView({ tools, toolUsage }: { tools: ToolKey[]; toolUsage: ToolUsageInfo }) {
  return (
    <Box>
      <Typography variant="subtitle2" sx={{ fontWeight: 600, mb: 1 }}>
        Native Tools
      </Typography>
      <Stack spacing={1.5}>
        {/* Tool badges */}
        <Box sx={{ display: 'flex', gap: 1, flexWrap: 'wrap', alignItems: 'center' }}>
          {tools.map((key) => {
            const Icon = getToolIcon(key);
            const used = isUsed(toolUsage, key);
            const displayName = getToolDisplayName(key);

            return (
              <Box
                key={key}
                sx={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 0.5,
                  px: 1.5,
                  py: 0.5,
                  bgcolor: used ? 'action.selected' : 'action.hover',
                  border: 1,
                  borderColor: used ? 'primary.main' : 'divider',
                  borderRadius: 1,
                  fontSize: '0.75rem',
                  fontWeight: used ? 600 : 500,
                }}
              >
                <Icon sx={{ fontSize: '1rem' }} />
                <Typography variant="caption" sx={{ fontWeight: 'inherit' }}>
                  {displayName}
                </Typography>
                {used ? (
                  <Chip
                    label="Used"
                    size="small"
                    color="success"
                    sx={{
                      height: '16px',
                      fontSize: '0.65rem',
                      '& .MuiChip-label': { px: 0.5 },
                    }}
                  />
                ) : (
                  <Chip
                    label="Enabled"
                    size="small"
                    variant="outlined"
                    sx={{
                      height: '16px',
                      fontSize: '0.65rem',
                      '& .MuiChip-label': { px: 0.5 },
                    }}
                  />
                )}
              </Box>
            );
          })}
        </Box>

        {/* Usage detail sections */}
        {toolUsage.google_search && (
          <Accordion defaultExpanded sx={{ boxShadow: 'none', border: 1, borderColor: 'divider' }}>
            <AccordionSummary expandIcon={<ExpandMoreIcon />}>
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                <Typography variant="body2" sx={{ fontWeight: 600 }}>
                  Google Search Usage
                </Typography>
                <Chip
                  label={`${toolUsage.google_search.queries.length} queries, ${toolUsage.google_search.sourcesCount} sources`}
                  size="small"
                  color="primary"
                  sx={{ height: '20px', fontSize: '0.7rem' }}
                />
              </Box>
            </AccordionSummary>
            <AccordionDetails>
              <Stack spacing={1}>
                <Typography variant="caption" color="text.secondary" sx={{ fontWeight: 600 }}>
                  Queries:
                </Typography>
                {toolUsage.google_search.queries.map((query, idx) => (
                  <Box
                    key={idx}
                    sx={{
                      p: 1,
                      bgcolor: 'grey.50',
                      borderRadius: 1,
                      border: 1,
                      borderColor: 'divider',
                    }}
                  >
                    <Typography variant="body2" sx={{ fontFamily: 'monospace', fontSize: '0.8rem' }}>
                      {query}
                    </Typography>
                  </Box>
                ))}
              </Stack>
            </AccordionDetails>
          </Accordion>
        )}

        {toolUsage.url_context && (
          <Accordion defaultExpanded sx={{ boxShadow: 'none', border: 1, borderColor: 'divider' }}>
            <AccordionSummary expandIcon={<ExpandMoreIcon />}>
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                <Typography variant="body2" sx={{ fontWeight: 600 }}>
                  URL Context Usage
                </Typography>
                <Chip
                  label={`${toolUsage.url_context.urls.length} URLs`}
                  size="small"
                  color="info"
                  sx={{ height: '20px', fontSize: '0.7rem' }}
                />
              </Box>
            </AccordionSummary>
            <AccordionDetails>
              <Stack spacing={1}>
                {toolUsage.url_context.urls.map((url, idx) => (
                  <Box
                    key={idx}
                    sx={{
                      p: 1,
                      bgcolor: 'grey.50',
                      borderRadius: 1,
                      border: 1,
                      borderColor: 'divider',
                    }}
                  >
                    {url.title && (
                      <Typography variant="body2" sx={{ fontWeight: 600, mb: 0.5 }}>
                        {url.title}
                      </Typography>
                    )}
                    <Typography
                      variant="body2"
                      sx={{
                        fontFamily: 'monospace',
                        fontSize: '0.75rem',
                        color: 'primary.main',
                        wordBreak: 'break-all',
                      }}
                    >
                      {url.uri}
                    </Typography>
                  </Box>
                ))}
              </Stack>
            </AccordionDetails>
          </Accordion>
        )}

        {toolUsage.code_execution && (
          <Accordion defaultExpanded sx={{ boxShadow: 'none', border: 1, borderColor: 'divider' }}>
            <AccordionSummary expandIcon={<ExpandMoreIcon />}>
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                <Typography variant="body2" sx={{ fontWeight: 600 }}>
                  Code Execution Usage
                </Typography>
                <Chip
                  label={`${toolUsage.code_execution.count} executions`}
                  size="small"
                  color="secondary"
                  sx={{ height: '20px', fontSize: '0.7rem' }}
                />
              </Box>
            </AccordionSummary>
            <AccordionDetails>
              <Typography variant="caption" color="text.secondary" sx={{ fontStyle: 'italic' }}>
                Python code was executed during response generation
              </Typography>
            </AccordionDetails>
          </Accordion>
        )}
      </Stack>
    </Box>
  );
}

/* ── Helpers ──────────────────────────────────────────────────────────── */

function isUsed(usage: ToolUsageInfo, key: ToolKey): boolean {
  switch (key) {
    case TOOL_KEYS.GOOGLE_SEARCH:
      return !!usage.google_search;
    case TOOL_KEYS.CODE_EXECUTION:
      return !!usage.code_execution;
    case TOOL_KEYS.URL_CONTEXT:
      return !!usage.url_context;
  }
}

export default memo(NativeToolsDisplay);
