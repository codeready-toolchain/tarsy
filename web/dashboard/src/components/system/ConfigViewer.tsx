/**
 * Configuration viewer — read-only effective config from GET /api/v1/system/config.
 *
 * Primary: structured browsable sections.
 * Secondary: JSON/YAML of the same sanitized DTO with copy.
 * Syntax coloring reuses JsonDisplay styles (react-json-view-lite + highlightYaml).
 */

import { useState, useEffect, useCallback, useMemo, type ReactNode } from 'react';
import Accordion from '@mui/material/Accordion';
import AccordionDetails from '@mui/material/AccordionDetails';
import AccordionSummary from '@mui/material/AccordionSummary';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Chip from '@mui/material/Chip';
import CircularProgress from '@mui/material/CircularProgress';
import Collapse from '@mui/material/Collapse';
import IconButton from '@mui/material/IconButton';
import Paper from '@mui/material/Paper';
import ToggleButton from '@mui/material/ToggleButton';
import ToggleButtonGroup from '@mui/material/ToggleButtonGroup';
import Typography from '@mui/material/Typography';
import {
  ExpandMore as ExpandMoreIcon,
  Refresh as RefreshIcon,
} from '@mui/icons-material';
import { dump as dumpYaml } from 'js-yaml';
import { JsonView, allExpanded, defaultStyles } from 'react-json-view-lite';
import 'react-json-view-lite/dist/index.css';
import '../shared/JsonDisplay/JsonDisplay.css';
import CopyButton from '../shared/CopyButton';
import { highlightYaml } from '../shared/JsonDisplay/utils';
import { getSystemConfig, getSystemConfigSkill, handleAPIError } from '../../services/api.ts';
import type {
  SystemConfigResponse,
  SkillMetaView,
  MCPServerConfigView,
  AgentConfigView,
  ChainConfigView,
  LLMProviderConfigView,
} from '../../types/system.ts';

type ViewMode = 'structured' | 'json' | 'yaml';

const jsonViewStyle = {
  ...defaultStyles,
  container: 'json-custom-container',
  stringValue: 'json-string-value',
  numberValue: 'json-number-value',
  booleanValue: 'json-boolean-value',
  nullValue: 'json-null-value',
  label: 'json-label',
  punctuation: 'json-punctuation',
};

const codePanelSx = {
  p: 2,
  maxHeight: '70vh',
  overflow: 'auto',
  fontFamily: 'Monaco, Menlo, "Ubuntu Mono", monospace',
  fontSize: '0.8rem',
  bgcolor: 'action.hover',
  '& .json-view-wrapper': {
    wordBreak: 'break-word',
    overflowWrap: 'break-word',
    whiteSpace: 'normal',
  },
} as const;

function HighlightedJson({ data, maxHeight }: { data: object; maxHeight?: string | number }) {
  return (
    <Paper
      variant="outlined"
      sx={{
        ...codePanelSx,
        maxHeight: maxHeight ?? codePanelSx.maxHeight,
      }}
    >
      <JsonView data={data} shouldExpandNode={allExpanded} style={jsonViewStyle} />
    </Paper>
  );
}

function HighlightedYaml({ source, maxHeight }: { source: string; maxHeight?: string | number }) {
  const html = useMemo(() => highlightYaml(source), [source]);
  return (
    <Paper
      variant="outlined"
      component="pre"
      sx={{
        ...codePanelSx,
        maxHeight: maxHeight ?? codePanelSx.maxHeight,
        m: 0,
        whiteSpace: 'pre-wrap',
        wordBreak: 'break-word',
      }}
      dangerouslySetInnerHTML={{ __html: html }}
    />
  );
}

export function ConfigViewer() {
  const [config, setConfig] = useState<SystemConfigResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [viewMode, setViewMode] = useState<ViewMode>('structured');

  const fetchConfig = useCallback(async () => {
    try {
      const data = await getSystemConfig();
      setConfig(data);
      setError(null);
    } catch (err) {
      setError(handleAPIError(err));
      console.error('Failed to fetch system config:', err);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchConfig();
  }, [fetchConfig]);

  const handleRefresh = () => {
    setLoading(true);
    fetchConfig();
  };

  if (loading && !config) {
    return (
      <Box sx={{ display: 'flex', justifyContent: 'center', py: 6 }}>
        <CircularProgress />
      </Box>
    );
  }

  if (error && !config) {
    return (
      <Alert
        severity="error"
        action={
          <IconButton size="small" onClick={handleRefresh} aria-label="Retry">
            <RefreshIcon />
          </IconButton>
        }
      >
        {error}
      </Alert>
    );
  }

  if (!config) {
    return (
      <Paper sx={{ p: 3 }}>
        <Typography color="text.secondary">No configuration available.</Typography>
      </Paper>
    );
  }

  const serialized =
    viewMode === 'yaml'
      ? dumpYaml(config, { lineWidth: 120, noRefs: true, sortKeys: true })
      : JSON.stringify(config, null, 2);

  return (
    <Box>
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: 2, flexWrap: 'wrap', gap: 1 }}>
        <Typography variant="h6">Effective Configuration</Typography>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          <ToggleButtonGroup
            size="small"
            exclusive
            value={viewMode}
            onChange={(_, v: ViewMode | null) => {
              if (v) setViewMode(v);
            }}
            aria-label="Config view mode"
          >
            <ToggleButton value="structured">Structured</ToggleButton>
            <ToggleButton value="json">JSON</ToggleButton>
            <ToggleButton value="yaml">YAML</ToggleButton>
          </ToggleButtonGroup>
          {(viewMode === 'json' || viewMode === 'yaml') && (
            <CopyButton text={serialized} variant="icon" size="small" tooltip={`Copy ${viewMode.toUpperCase()}`} />
          )}
          <IconButton onClick={handleRefresh} size="small" aria-label="Refresh">
            <RefreshIcon />
          </IconButton>
        </Box>
      </Box>

      {error && (
        <Alert severity="warning" sx={{ mb: 2 }}>
          {error}
        </Alert>
      )}

      {viewMode === 'structured' && <StructuredConfig config={config} />}
      {viewMode === 'json' && <HighlightedJson data={config} />}
      {viewMode === 'yaml' && <HighlightedYaml source={serialized} />}
    </Box>
  );
}

function StructuredConfig({ config }: { config: SystemConfigResponse }) {
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
      <ConfigSection title="Defaults" count={config.defaults ? 1 : 0}>
        {config.defaults ? (
          <KeyValueBlock data={config.defaults} />
        ) : (
          <EmptyNote />
        )}
      </ConfigSection>

      <ConfigSection title="Queue" count={config.queue ? 1 : 0}>
        {config.queue ? <KeyValueBlock data={config.queue} /> : <EmptyNote />}
      </ConfigSection>

      <ConfigSection title="System" count={1}>
        <KeyValueBlock data={config.system} />
      </ConfigSection>

      <ConfigSection title="Agents" count={Object.keys(config.agents).length}>
        {Object.keys(config.agents).length === 0 ? (
          <EmptyNote />
        ) : (
          Object.entries(config.agents).map(([name, agent]) => (
            <NamedEntry key={name} name={name}>
              <AgentDetails agent={agent} />
            </NamedEntry>
          ))
        )}
      </ConfigSection>

      <ConfigSection title="Chains" count={Object.keys(config.chains).length}>
        {Object.keys(config.chains).length === 0 ? (
          <EmptyNote />
        ) : (
          Object.entries(config.chains).map(([id, chain]) => (
            <NamedEntry key={id} name={id}>
              <ChainDetails chain={chain} />
            </NamedEntry>
          ))
        )}
      </ConfigSection>

      <ConfigSection title="MCP Servers" count={Object.keys(config.mcp_servers).length}>
        {Object.keys(config.mcp_servers).length === 0 ? (
          <EmptyNote />
        ) : (
          Object.entries(config.mcp_servers).map(([id, server]) => (
            <NamedEntry key={id} name={id}>
              <MCPServerDetails server={server} />
            </NamedEntry>
          ))
        )}
      </ConfigSection>

      <ConfigSection title="LLM Providers" count={Object.keys(config.llm_providers).length}>
        {Object.keys(config.llm_providers).length === 0 ? (
          <EmptyNote />
        ) : (
          Object.entries(config.llm_providers).map(([id, provider]) => (
            <NamedEntry key={id} name={id}>
              <LLMProviderDetails provider={provider} />
            </NamedEntry>
          ))
        )}
      </ConfigSection>

      <ConfigSection title="Skills" count={Object.keys(config.skills).length}>
        {Object.keys(config.skills).length === 0 ? (
          <EmptyNote />
        ) : (
          Object.entries(config.skills).map(([name, skill]) => (
            <SkillEntry key={name} skill={skill} />
          ))
        )}
      </ConfigSection>
    </Box>
  );
}

function ConfigSection({
  title,
  count,
  children,
}: {
  title: string;
  count: number;
  children: ReactNode;
}) {
  return (
    <Accordion defaultExpanded={title === 'Agents' || title === 'Chains' || title === 'MCP Servers'}>
      <AccordionSummary expandIcon={<ExpandMoreIcon />}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          <Typography fontWeight={600}>{title}</Typography>
          <Chip size="small" label={count} variant="outlined" />
        </Box>
      </AccordionSummary>
      <AccordionDetails sx={{ pt: 0 }}>{children}</AccordionDetails>
    </Accordion>
  );
}

function NamedEntry({ name, children }: { name: string; children: ReactNode }) {
  return (
    <Paper variant="outlined" sx={{ p: 1.5, mb: 1 }}>
      <Typography variant="subtitle2" sx={{ fontFamily: 'monospace', mb: 1 }}>
        {name}
      </Typography>
      {children}
    </Paper>
  );
}

function EmptyNote() {
  return (
    <Typography variant="body2" color="text.secondary">
      None configured.
    </Typography>
  );
}

function Field({ label, value }: { label: string; value: ReactNode }) {
  if (value === undefined || value === null || value === '') return null;
  return (
    <Box sx={{ display: 'flex', gap: 1, mb: 0.5, flexWrap: 'wrap' }}>
      <Typography variant="caption" color="text.secondary" sx={{ minWidth: 140 }}>
        {label}
      </Typography>
      <Typography variant="body2" component="div" sx={{ fontFamily: 'monospace', fontSize: '0.85rem', flex: 1 }}>
        {value}
      </Typography>
    </Box>
  );
}

function ChipList({ items }: { items?: string[] | null }) {
  if (!items || items.length === 0) return <Typography variant="body2" color="text.secondary">—</Typography>;
  return (
    <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.5 }}>
      {items.map((item) => (
        <Chip key={item} size="small" label={item} variant="outlined" />
      ))}
    </Box>
  );
}

function KeyValueBlock({ data }: { data: unknown }) {
  if (data !== null && typeof data === 'object') {
    return <HighlightedJson data={data as object} maxHeight={360} />;
  }
  return (
    <Typography variant="body2" sx={{ fontFamily: 'monospace', fontSize: '0.85rem' }}>
      {String(data)}
    </Typography>
  );
}

function AgentDetails({ agent }: { agent: AgentConfigView }) {
  return (
    <Box>
      <Field label="type" value={agent.type || '(default)'} />
      <Field label="description" value={agent.description} />
      <Field label="llm_backend" value={agent.llm_backend} />
      <Field label="max_iterations" value={agent.max_iterations} />
      <Field label="mcp_servers" value={<ChipList items={agent.mcp_servers} />} />
      <Field label="required_skills" value={<ChipList items={agent.required_skills} />} />
      <Field
        label="skills"
        value={
          agent.skills === null || agent.skills === undefined
            ? '(all registry skills)'
            : agent.skills.length === 0
              ? '(none)'
              : <ChipList items={agent.skills} />
        }
      />
      {agent.custom_instructions && (
        <Box sx={{ mt: 1 }}>
          <Typography variant="caption" color="text.secondary">
            custom_instructions
          </Typography>
          <Box
            sx={{
              mt: 0.5,
              p: 1,
              borderRadius: 1,
              bgcolor: 'action.hover',
              fontSize: '0.85rem',
              whiteSpace: 'pre-wrap',
              maxHeight: 240,
              overflow: 'auto',
            }}
          >
            {agent.custom_instructions}
          </Box>
        </Box>
      )}
      {agent.orchestrator && (
        <Box sx={{ mt: 1 }}>
          <Typography variant="caption" color="text.secondary">
            orchestrator
          </Typography>
          <KeyValueBlock data={agent.orchestrator} />
        </Box>
      )}
      {agent.native_tools && Object.keys(agent.native_tools).length > 0 && (
        <Box sx={{ mt: 1 }}>
          <Typography variant="caption" color="text.secondary">
            native_tools
          </Typography>
          <KeyValueBlock data={agent.native_tools} />
        </Box>
      )}
    </Box>
  );
}

function ChainDetails({ chain }: { chain: ChainConfigView }) {
  return (
    <Box>
      <Field label="alert_types" value={<ChipList items={chain.alert_types} />} />
      <Field label="description" value={chain.description} />
      <Field label="llm_provider" value={chain.llm_provider} />
      <Field label="llm_backend" value={chain.llm_backend} />
      <Field label="max_iterations" value={chain.max_iterations} />
      <Field label="mcp_servers" value={<ChipList items={chain.mcp_servers} />} />
      <Box sx={{ mt: 1 }}>
        <Typography variant="caption" color="text.secondary">
          stages ({chain.stages?.length ?? 0})
        </Typography>
        <KeyValueBlock data={chain.stages} />
      </Box>
      {chain.chat && (
        <Box sx={{ mt: 1 }}>
          <Typography variant="caption" color="text.secondary">
            chat
          </Typography>
          <KeyValueBlock data={chain.chat} />
        </Box>
      )}
      {chain.scoring && (
        <Box sx={{ mt: 1 }}>
          <Typography variant="caption" color="text.secondary">
            scoring
          </Typography>
          <KeyValueBlock data={chain.scoring} />
        </Box>
      )}
    </Box>
  );
}

function MCPServerDetails({ server }: { server: MCPServerConfigView }) {
  const t = server.transport;
  return (
    <Box>
      <Field label="transport.type" value={t.type} />
      <Field label="transport.command" value={t.command} />
      <Field label="transport.args" value={t.args?.join(', ')} />
      <Field label="transport.url" value={t.url} />
      <Field label="transport.timeout" value={t.timeout} />
      <Field label="transport.verify_ssl" value={t.verify_ssl === undefined || t.verify_ssl === null ? undefined : String(t.verify_ssl)} />
      <Field label="env_keys" value={<ChipList items={t.env_keys} />} />
      <Field label="bearer_token_set" value={String(t.bearer_token_set)} />
      {server.instructions && (
        <Box sx={{ mt: 1 }}>
          <Typography variant="caption" color="text.secondary">
            instructions
          </Typography>
          <Box
            sx={{
              mt: 0.5,
              p: 1,
              borderRadius: 1,
              bgcolor: 'action.hover',
              fontSize: '0.85rem',
              whiteSpace: 'pre-wrap',
              maxHeight: 240,
              overflow: 'auto',
            }}
          >
            {server.instructions}
          </Box>
        </Box>
      )}
      {server.data_masking && (
        <Box sx={{ mt: 1 }}>
          <Typography variant="caption" color="text.secondary">
            data_masking
          </Typography>
          <KeyValueBlock data={server.data_masking} />
        </Box>
      )}
      {server.summarization && (
        <Box sx={{ mt: 1 }}>
          <Typography variant="caption" color="text.secondary">
            summarization
          </Typography>
          <KeyValueBlock data={server.summarization} />
        </Box>
      )}
    </Box>
  );
}

function LLMProviderDetails({ provider }: { provider: LLMProviderConfigView }) {
  return (
    <Box>
      <Field label="type" value={provider.type} />
      <Field label="model" value={provider.model} />
      <Field label="api_key_env" value={provider.api_key_env} />
      <Field label="credentials_env" value={provider.credentials_env} />
      <Field label="project_env" value={provider.project_env} />
      <Field label="location_env" value={provider.location_env} />
      <Field label="base_url" value={provider.base_url} />
      <Field label="max_tool_result_tokens" value={provider.max_tool_result_tokens} />
      {provider.native_tools && Object.keys(provider.native_tools).length > 0 && (
        <Box sx={{ mt: 1 }}>
          <Typography variant="caption" color="text.secondary">
            native_tools
          </Typography>
          <KeyValueBlock data={provider.native_tools} />
        </Box>
      )}
    </Box>
  );
}

function SkillEntry({ skill }: { skill: SkillMetaView }) {
  const [expanded, setExpanded] = useState(false);
  const [body, setBody] = useState<string | null>(null);
  const [loadingBody, setLoadingBody] = useState(false);
  const [bodyError, setBodyError] = useState<string | null>(null);

  const handleToggle = async () => {
    const next = !expanded;
    setExpanded(next);
    if (next && body === null && !loadingBody) {
      setLoadingBody(true);
      setBodyError(null);
      try {
        const detail = await getSystemConfigSkill(skill.name);
        setBody(detail.body);
      } catch (err) {
        setBodyError(handleAPIError(err));
      } finally {
        setLoadingBody(false);
      }
    }
  };

  return (
    <Paper variant="outlined" sx={{ mb: 1 }}>
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: 1,
          p: 1.5,
          cursor: 'pointer',
          '&:hover': { bgcolor: 'action.hover' },
        }}
        onClick={handleToggle}
      >
        <Box sx={{ flex: 1, minWidth: 0 }}>
          <Typography variant="subtitle2" sx={{ fontFamily: 'monospace' }}>
            {skill.name}
          </Typography>
          {skill.description && (
            <Typography variant="body2" color="text.secondary" noWrap>
              {skill.description}
            </Typography>
          )}
        </Box>
        <IconButton size="small" aria-label={expanded ? 'Collapse skill' : 'Expand skill'}>
          <ExpandMoreIcon
            sx={{
              transform: expanded ? 'rotate(180deg)' : 'none',
              transition: 'transform 0.2s',
            }}
          />
        </IconButton>
      </Box>
      <Collapse in={expanded}>
        <Box sx={{ px: 1.5, pb: 1.5, borderTop: 1, borderColor: 'divider' }}>
          {loadingBody && (
            <Box sx={{ display: 'flex', justifyContent: 'center', py: 2 }}>
              <CircularProgress size={24} />
            </Box>
          )}
          {bodyError && (
            <Alert severity="error" sx={{ mt: 1 }}>
              {bodyError}
            </Alert>
          )}
          {body !== null && (
            <Box sx={{ mt: 1 }}>
              <Box sx={{ display: 'flex', justifyContent: 'flex-end', mb: 0.5 }}>
                <CopyButton text={body} variant="icon" size="small" tooltip="Copy skill body" />
              </Box>
              <Box
                sx={{
                  p: 1.5,
                  borderRadius: 1,
                  bgcolor: 'action.hover',
                  fontFamily: 'monospace',
                  fontSize: '0.8rem',
                  whiteSpace: 'pre-wrap',
                  maxHeight: 400,
                  overflow: 'auto',
                }}
              >
                {body || '(empty body)'}
              </Box>
            </Box>
          )}
        </Box>
      </Collapse>
    </Paper>
  );
}
