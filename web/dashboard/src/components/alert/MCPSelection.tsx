/**
 * MCP Server/Tool Selection Component
 *
 * Allows users to customize which MCP servers, tools, and native tools to use
 * for alert processing. Shows default configuration and detects changes.
 * Only sends override config when user modifies the defaults.
 *
 * Visual layer ported from old TARSy dashboard MCPSelection.tsx.
 * Data layer rewritten for new Go backend endpoints.
 */

import { useState, useEffect, useMemo, useRef, useCallback } from 'react';
import {
  Box,
  Accordion,
  AccordionSummary,
  AccordionDetails,
  Card,
  CardContent,
  Typography,
  Checkbox,
  FormControlLabel,
  Button,
  Chip,
  Collapse,
  Stack,
  Alert as MuiAlert,
  CircularProgress,
  Divider,
} from '@mui/material';
import {
  ExpandMore as ExpandMoreIcon,
  Settings as SettingsIcon,
  ChevronRight as ChevronRightIcon,
  InfoOutlined as InfoIcon,
  RestartAlt as RestartAltIcon,
} from '@mui/icons-material';

import type {
  MCPSelectionConfig,
  MCPServerStatus,
  NativeToolsConfig,
} from '../../types/system.ts';
import { getDefaultTools, getMCPServers } from '../../services/api.ts';
import {
  NATIVE_TOOL_NAMES,
  NATIVE_TOOL_LABELS,
  NATIVE_TOOL_DESCRIPTIONS,
  type NativeToolName,
} from '../../constants/nativeTools.ts';

interface MCPSelectionProps {
  value: MCPSelectionConfig | undefined;
  onChange: (config: MCPSelectionConfig | undefined) => void;
  disabled?: boolean;
  alertType?: string;
}

// ────────────────────────────────────────────────────────────
// Deep equality check for MCPSelectionConfig
// ────────────────────────────────────────────────────────────

function configsAreEqual(
  a: MCPSelectionConfig | null | undefined,
  b: MCPSelectionConfig | null | undefined,
): boolean {
  if (a === b) return true;
  if (!a || !b) return a === b;

  // Compare servers
  if (a.servers.length !== b.servers.length) return false;

  const aSorted = [...a.servers].sort((x, y) => x.name.localeCompare(y.name));
  const bSorted = [...b.servers].sort((x, y) => x.name.localeCompare(y.name));

  for (let i = 0; i < aSorted.length; i++) {
    if (aSorted[i].name !== bSorted[i].name) return false;

    const aTools = aSorted[i].tools;
    const bTools = bSorted[i].tools;

    // Both null/undefined = same (all tools selected)
    if (aTools == null && bTools == null) continue;
    // One null/undefined, other is array = different
    if (aTools == null || bTools == null) return false;
    // Both are arrays — compare them
    if (aTools.length !== bTools.length) return false;

    const aToolsSorted = [...aTools].sort();
    const bToolsSorted = [...bTools].sort();
    if (JSON.stringify(aToolsSorted) !== JSON.stringify(bToolsSorted)) return false;
  }

  // Compare native_tools
  const aNative = a.native_tools || {};
  const bNative = b.native_tools || {};

  return (
    aNative.google_search === bNative.google_search &&
    aNative.code_execution === bNative.code_execution &&
    aNative.url_context === bNative.url_context
  );
}

// ────────────────────────────────────────────────────────────
// Component
// ────────────────────────────────────────────────────────────

export function MCPSelection({
  value,
  onChange,
  disabled = false,
  alertType,
}: MCPSelectionProps) {
  // State for defaults and current config
  const [defaultConfig, setDefaultConfig] = useState<MCPSelectionConfig | null>(null);
  const [currentConfig, setCurrentConfig] = useState<MCPSelectionConfig | null>(null);
  const [hasChanges, setHasChanges] = useState(false);

  // State for available servers (for tool details)
  const [availableServers, setAvailableServers] = useState<MCPServerStatus[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // State for UI expansion
  const [expanded, setExpanded] = useState(false);
  const [expandedServers, setExpandedServers] = useState<Set<string>>(new Set());
  const [nativeToolsExpanded, setNativeToolsExpanded] = useState(false);

  // Track if component was initialized with a value (e.g., from resubmit)
  const initializedWithValueRef = useRef(value !== undefined);

  // Track if defaults have been loaded (to avoid premature onChange calls)
  const defaultsLoadedRef = useRef(false);

  // Guard against stale async completions: only the latest invocation may
  // touch state. Each call to loadDefaultsAndServers mints a new ID and
  // stores it in latestRequestIdRef; before every state update the callback
  // checks that its local ID still matches.
  const latestRequestIdRef = useRef(0);

  // ── Load defaults and server details ────────────────────

  const loadDefaultsAndServers = useCallback(async () => {
    const requestId = ++latestRequestIdRef.current;
    const requestAlertType = alertType;

    setLoading(true);
    setError(null);

    try {
      const [defaultsResp, serversResp] = await Promise.all([
        getDefaultTools(requestAlertType),
        getMCPServers(),
      ]);

      // Stale response — a newer request has been issued
      if (latestRequestIdRef.current !== requestId) {
        return;
      }

      // Build default MCPSelectionConfig from the response
      const defaults: MCPSelectionConfig = {
        servers: defaultsResp.mcp_servers.map((id) => ({
          name: id,
          tools: null, // null = all tools
        })),
        native_tools: defaultsResp.native_tools as NativeToolsConfig,
      };

      setDefaultConfig(defaults);
      setAvailableServers(serversResp.servers);

      defaultsLoadedRef.current = true;

      setExpandedServers(new Set());
      setNativeToolsExpanded(false);
    } catch (err: unknown) {
      if (latestRequestIdRef.current !== requestId) return;
      const message = err instanceof Error ? err.message : 'Failed to load configuration.';
      setError(message);
    } finally {
      // Only clear loading if this is still the latest request
      if (latestRequestIdRef.current === requestId) {
        setLoading(false);
      }
    }
  }, [alertType]);

  // Load on first expansion or when alert type changes
  useEffect(() => {
    if (expanded && !loading && !error) {
      loadDefaultsAndServers();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [expanded, alertType]);

  // Reconcile value prop and defaultConfig to set currentConfig
  useEffect(() => {
    if (value !== undefined) {
      setCurrentConfig(value);
    } else if (defaultConfig !== null) {
      setCurrentConfig(defaultConfig);
    } else {
      setCurrentConfig(null);
    }
  }, [value, defaultConfig]);

  // Detect changes whenever currentConfig changes
  useEffect(() => {
    const changed = !configsAreEqual(currentConfig, defaultConfig);
    setHasChanges(changed);

    if (!defaultsLoadedRef.current) return;

    if (!changed && !initializedWithValueRef.current) {
      onChange(undefined);
    } else {
      onChange(currentConfig || undefined);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentConfig, defaultConfig]);

  // ── Handlers ────────────────────────────────────────────

  const handleResetToDefaults = () => {
    setCurrentConfig(defaultConfig);
    setExpandedServers(new Set());
    setNativeToolsExpanded(false);
    initializedWithValueRef.current = false;
  };

  const handleServerToggle = (serverId: string) => {
    if (!currentConfig) return;

    const newServers = [...currentConfig.servers];
    const existingIndex = newServers.findIndex((s) => s.name === serverId);

    if (existingIndex >= 0) {
      newServers.splice(existingIndex, 1);
      const newExpanded = new Set(expandedServers);
      newExpanded.delete(serverId);
      setExpandedServers(newExpanded);
    } else {
      newServers.push({ name: serverId, tools: null });
    }

    setCurrentConfig({ ...currentConfig, servers: newServers });
  };

  const toggleToolExpansion = (serverId: string) => {
    const newExpanded = new Set(expandedServers);
    if (newExpanded.has(serverId)) {
      newExpanded.delete(serverId);
    } else {
      newExpanded.add(serverId);
    }
    setExpandedServers(newExpanded);
  };

  const handleAllToolsToggle = (serverId: string, checked: boolean) => {
    if (!currentConfig) return;

    const newServers = currentConfig.servers.map((server) => {
      if (server.name === serverId) {
        return { ...server, tools: checked ? null : ([] as string[]) };
      }
      return server;
    });

    setCurrentConfig({ ...currentConfig, servers: newServers });
  };

  const handleToolToggle = (serverId: string, toolName: string) => {
    if (!currentConfig) return;

    const newServers = currentConfig.servers.map((server) => {
      if (server.name === serverId) {
        let newTools: string[] | null;

        if (server.tools === null) {
          // Was "all tools" — create array with all except this one
          const serverInfo = availableServers.find((s) => s.id === serverId);
          if (serverInfo) {
            newTools = serverInfo.tools.map((t) => t.name).filter((t) => t !== toolName);
          } else {
            newTools = null;
          }
        } else {
          const toolSet = new Set(server.tools);
          if (toolSet.has(toolName)) {
            toolSet.delete(toolName);
          } else {
            toolSet.add(toolName);
          }
          newTools = Array.from(toolSet);
        }

        return { ...server, tools: newTools };
      }
      return server;
    });

    setCurrentConfig({ ...currentConfig, servers: newServers });
  };

  const handleNativeToolToggle = (toolName: NativeToolName) => {
    if (!currentConfig) return;

    const currentNativeTools = currentConfig.native_tools || {};
    const newNativeTools = {
      ...currentNativeTools,
      [toolName]: !currentNativeTools[toolName],
    };

    setCurrentConfig({ ...currentConfig, native_tools: newNativeTools });
  };

  // ── Helpers ─────────────────────────────────────────────

  const isServerSelected = (serverId: string): boolean => {
    return currentConfig?.servers.some((s) => s.name === serverId) || false;
  };

  const areAllToolsSelected = (serverId: string): boolean => {
    const server = currentConfig?.servers.find((s) => s.name === serverId);
    return server?.tools === null;
  };

  const isToolSelected = (serverId: string, toolName: string): boolean => {
    const server = currentConfig?.servers.find((s) => s.name === serverId);
    if (!server) return false;
    if (server.tools === null) return true;
    if (!server.tools) return false;
    return server.tools.includes(toolName);
  };

  const hasNoToolsSelected = (serverId: string): boolean => {
    const server = currentConfig?.servers.find((s) => s.name === serverId);
    if (!server) return false;
    return Array.isArray(server.tools) && server.tools.length === 0;
  };

  const isNativeToolEnabled = (toolName: NativeToolName): boolean => {
    return currentConfig?.native_tools?.[toolName] || false;
  };

  const enabledNativeToolsCount = useMemo(() => {
    return currentConfig?.native_tools
      ? Object.values(currentConfig.native_tools).filter(Boolean).length
      : 0;
  }, [currentConfig?.native_tools]);

  // ── Render ──────────────────────────────────────────────

  return (
    <Box sx={{ px: 4, py: 2 }}>
      <Accordion
        expanded={expanded}
        onChange={(_, isExpanded) => setExpanded(isExpanded)}
        disabled={disabled}
        sx={{
          boxShadow: expanded ? '0 1px 4px rgba(0, 0, 0, 0.08)' : 'none',
          borderRadius: 2,
          border: '1px solid',
          borderColor: 'divider',
          bgcolor: 'rgba(25, 118, 210, 0.04)',
          transition: 'all 0.2s ease-in-out',
          '&:before': { display: 'none' },
          '&:hover': {
            borderColor: 'primary.light',
            bgcolor: 'rgba(25, 118, 210, 0.06)',
          },
        }}
      >
        <AccordionSummary
          expandIcon={<ExpandMoreIcon sx={{ color: 'primary.main' }} />}
          sx={{
            px: 2,
            py: 1.5,
            minHeight: '56px',
            borderRadius: expanded ? '8px 8px 0 0' : '8px',
            bgcolor: 'transparent',
            transition: 'background-color 0.2s ease-in-out',
            '& .MuiAccordionSummary-content': { alignItems: 'center', gap: 1.5 },
            '&:hover': { bgcolor: 'rgba(25, 118, 210, 0.06)' },
          }}
        >
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              width: 36,
              height: 36,
              borderRadius: '8px',
              bgcolor: 'primary.main',
              color: 'white',
            }}
          >
            <SettingsIcon sx={{ fontSize: 20 }} />
          </Box>
          <Box sx={{ flex: 1 }}>
            <Typography
              variant="subtitle1"
              sx={{
                color: 'primary.main',
                fontWeight: 700,
                fontSize: '0.95rem',
                lineHeight: 1.2,
              }}
            >
              Advanced: Tools Selection
            </Typography>
            <Typography
              variant="caption"
              sx={{ color: 'text.secondary', fontSize: '0.75rem', display: 'block', mt: 0.25 }}
            >
              Customize tools for this session. Uncheck to use defaults.
            </Typography>
          </Box>
          <Stack direction="row" spacing={0.5} sx={{ ml: 1 }}>
            {hasChanges ? (
              <Chip
                label="Custom"
                size="small"
                color="warning"
                sx={{ height: 24, fontWeight: 600, '& .MuiChip-label': { px: 1.5 } }}
              />
            ) : (
              <Chip
                label="Default"
                size="small"
                color="success"
                variant="outlined"
                sx={{ height: 24, fontWeight: 600, '& .MuiChip-label': { px: 1.5 } }}
              />
            )}
          </Stack>
        </AccordionSummary>

        <AccordionDetails sx={{ px: 2, pt: 2, pb: 2 }}>
          {/* Loading state */}
          {loading && (
            <Box sx={{ display: 'flex', justifyContent: 'center', py: 4 }}>
              <CircularProgress size={40} />
            </Box>
          )}

          {/* Error state */}
          {error && (
            <MuiAlert
              severity="error"
              sx={{ mb: 2, borderRadius: 2 }}
              action={
                <Button color="inherit" size="small" onClick={loadDefaultsAndServers}>
                  Retry
                </Button>
              }
            >
              {error}
            </MuiAlert>
          )}

          {/* Main content */}
          {!loading && !error && defaultConfig && (
            <>
              {/* Info and Reset */}
              <Box sx={{ mb: 3, display: 'flex', gap: 2, alignItems: 'flex-start' }}>
                <Box sx={{ display: 'flex', gap: 1, alignItems: 'flex-start', flex: 1 }}>
                  <InfoIcon sx={{ color: 'info.main', fontSize: 20, mt: 0.25 }} />
                  <Typography variant="body2" color="text.secondary" sx={{ lineHeight: 1.6 }}>
                    {hasChanges ? (
                      <>
                        <strong style={{ color: '#ed6c02' }}>Custom configuration active.</strong>{' '}
                        Your changes will override provider defaults.
                      </>
                    ) : (
                      <>Using provider defaults. Make changes to customize for this session.</>
                    )}
                  </Typography>
                </Box>
                {hasChanges && (
                  <Button
                    size="small"
                    variant="outlined"
                    startIcon={<RestartAltIcon />}
                    onClick={handleResetToDefaults}
                    disabled={disabled}
                    sx={{ textTransform: 'none', whiteSpace: 'nowrap' }}
                  >
                    Reset to Defaults
                  </Button>
                )}
              </Box>

              {/* Server cards */}
              <Stack spacing={2}>
                {availableServers.map((server) => {
                  const isSelected = isServerSelected(server.id);
                  const isToolExpanded = expandedServers.has(server.id);
                  const allToolsSelected = areAllToolsSelected(server.id);

                  return (
                    <Card
                      key={server.id}
                      elevation={0}
                      sx={{
                        border: '1px solid',
                        borderColor: isSelected ? 'primary.main' : 'divider',
                        borderRadius: 2,
                        bgcolor: isSelected ? 'rgba(25, 118, 210, 0.04)' : 'background.paper',
                        transition: 'all 0.2s',
                      }}
                    >
                      <CardContent sx={{ p: 2, '&:last-child': { pb: 2 } }}>
                        {/* Server header */}
                        <Box
                          sx={{
                            display: 'flex',
                            alignItems: 'center',
                            justifyContent: 'space-between',
                          }}
                        >
                          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flex: 1 }}>
                            <FormControlLabel
                              control={
                                <Checkbox
                                  checked={isSelected}
                                  onChange={() => handleServerToggle(server.id)}
                                  disabled={disabled}
                                />
                              }
                              label={
                                <Typography variant="body1" sx={{ fontWeight: 600 }}>
                                  {server.id}
                                </Typography>
                              }
                              sx={{ m: 0, flex: 1 }}
                            />
                          </Box>
                        </Box>

                        {/* Tool count */}
                        <Typography
                          variant="caption"
                          color="text.secondary"
                          sx={{ ml: 4, display: 'block', mt: 0.5 }}
                        >
                          {server.tools.length} tool{server.tools.length !== 1 ? 's' : ''}{' '}
                          available
                        </Typography>

                        {/* Warning when no tools selected */}
                        {isSelected && hasNoToolsSelected(server.id) && (
                          <MuiAlert
                            severity="warning"
                            sx={{ ml: 4, mt: 1, py: 0.5 }}
                            icon={<InfoIcon fontSize="small" />}
                          >
                            <Typography variant="caption">
                              No tools selected. Select at least one tool, otherwise this MCP server
                              won't be used.
                            </Typography>
                          </MuiAlert>
                        )}

                        {/* Tool selection */}
                        {isSelected && server.tools.length > 0 && (
                          <>
                            <Divider sx={{ my: 1.5, ml: 4 }} />
                            <Button
                              size="small"
                              startIcon={
                                isToolExpanded ? <ExpandMoreIcon /> : <ChevronRightIcon />
                              }
                              onClick={() => toggleToolExpansion(server.id)}
                              disabled={disabled}
                              sx={{
                                ml: 4,
                                textTransform: 'none',
                                color: 'primary.main',
                                fontWeight: 600,
                              }}
                            >
                              Select Specific Tools
                            </Button>

                            <Collapse in={isToolExpanded}>
                              <Box
                                sx={{
                                  mt: 2,
                                  ml: 4,
                                  p: 2,
                                  bgcolor: 'rgba(0, 0, 0, 0.02)',
                                  borderRadius: 1,
                                  border: '1px solid',
                                  borderColor: 'divider',
                                }}
                              >
                                {/* All tools checkbox */}
                                <Box
                                  sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 1 }}
                                >
                                  <Checkbox
                                    checked={allToolsSelected}
                                    onChange={(e) =>
                                      handleAllToolsToggle(server.id, e.target.checked)
                                    }
                                    disabled={disabled}
                                  />
                                  <Typography variant="body2" sx={{ fontWeight: 600 }}>
                                    All Tools
                                  </Typography>
                                </Box>

                                <Divider sx={{ mb: 1 }} />

                                {/* Individual tools */}
                                <Stack spacing={0.5} sx={{ maxHeight: 300, overflowY: 'auto' }}>
                                  {server.tools.map((tool) => (
                                    <FormControlLabel
                                      key={tool.name}
                                      control={
                                        <Checkbox
                                          checked={isToolSelected(server.id, tool.name)}
                                          onChange={() => handleToolToggle(server.id, tool.name)}
                                          disabled={disabled}
                                          size="small"
                                        />
                                      }
                                      label={
                                        <Box>
                                          <Typography
                                            variant="body2"
                                            sx={{
                                              fontFamily: 'monospace',
                                              fontSize: '0.85rem',
                                            }}
                                          >
                                            {tool.name}
                                          </Typography>
                                          {tool.description && (
                                            <Typography
                                              variant="caption"
                                              color="text.secondary"
                                              sx={{ display: 'block' }}
                                            >
                                              {tool.description}
                                            </Typography>
                                          )}
                                        </Box>
                                      }
                                      sx={{ m: 0, alignItems: 'flex-start' }}
                                    />
                                  ))}
                                </Stack>
                              </Box>
                            </Collapse>
                          </>
                        )}
                      </CardContent>
                    </Card>
                  );
                })}

                {/* Native Google Tools Section */}
                <Card
                  elevation={0}
                  sx={{
                    border: '1px solid',
                    borderColor: 'divider',
                    borderRadius: 2,
                    bgcolor: 'background.paper',
                    transition: 'all 0.2s',
                  }}
                >
                  <CardContent sx={{ p: 2, '&:last-child': { pb: 2 } }}>
                    <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                      <Typography variant="body1" sx={{ fontWeight: 600 }}>
                        Google Native Tools
                      </Typography>
                      <Chip
                        label="google/gemini"
                        size="small"
                        color="primary"
                        variant="outlined"
                        sx={{ height: 20, fontSize: '0.7rem' }}
                      />
                    </Box>

                    <Typography
                      variant="caption"
                      color="text.secondary"
                      sx={{ display: 'block', mt: 0.5 }}
                    >
                      {enabledNativeToolsCount} of 3 tools enabled
                    </Typography>

                    <Divider sx={{ my: 1.5 }} />
                    <Button
                      size="small"
                      startIcon={
                        nativeToolsExpanded ? <ExpandMoreIcon /> : <ChevronRightIcon />
                      }
                      onClick={() => setNativeToolsExpanded(!nativeToolsExpanded)}
                      disabled={disabled}
                      sx={{ textTransform: 'none', color: 'primary.main', fontWeight: 600 }}
                    >
                      Configure Tools
                    </Button>

                    <Collapse in={nativeToolsExpanded}>
                      <Box
                        sx={{
                          mt: 2,
                          p: 2,
                          bgcolor: 'rgba(0, 0, 0, 0.02)',
                          borderRadius: 1,
                          border: '1px solid',
                          borderColor: 'divider',
                        }}
                      >
                        <Stack spacing={1.5}>
                          {(
                            [
                              NATIVE_TOOL_NAMES.GOOGLE_SEARCH,
                              NATIVE_TOOL_NAMES.CODE_EXECUTION,
                              NATIVE_TOOL_NAMES.URL_CONTEXT,
                            ] as const
                          ).map((toolName) => (
                            <FormControlLabel
                              key={toolName}
                              control={
                                <Checkbox
                                  checked={isNativeToolEnabled(toolName)}
                                  onChange={() => handleNativeToolToggle(toolName)}
                                  disabled={disabled}
                                  size="small"
                                />
                              }
                              label={
                                <Box>
                                  <Typography
                                    variant="body2"
                                    sx={{ fontFamily: 'monospace', fontSize: '0.85rem' }}
                                  >
                                    {NATIVE_TOOL_LABELS[toolName]}
                                  </Typography>
                                  <Typography
                                    variant="caption"
                                    color="text.secondary"
                                    sx={{ display: 'block' }}
                                  >
                                    {NATIVE_TOOL_DESCRIPTIONS[toolName]}
                                  </Typography>
                                </Box>
                              }
                              sx={{ m: 0, alignItems: 'flex-start' }}
                            />
                          ))}
                        </Stack>
                      </Box>
                    </Collapse>
                  </CardContent>
                </Card>
              </Stack>
            </>
          )}
        </AccordionDetails>
      </Accordion>
    </Box>
  );
}
