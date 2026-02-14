/**
 * Manual Alert submission form component.
 *
 * Dual-mode input: Text (default) or Structured Key-Value pairs.
 * Supports runbook URL free-text input (Phase 8.1 will add browsing/dropdown).
 * Supports MCP server/tool selection override via MCPSelection component.
 * Supports resubmit pre-fill from location state (used by Session Detail).
 *
 * Visual layer ported from old TARSy dashboard ManualAlertForm.tsx.
 * Data layer rewritten for new Go backend (SubmitAlertRequest).
 */

import { useState, useEffect, useRef } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import {
  Box,
  Card,
  CardContent,
  Typography,
  TextField,
  MenuItem,
  Button,
  Stack,
  Alert as MuiAlert,
  CircularProgress,
  IconButton,
  Paper,
} from '@mui/material';
import {
  Send as SendIcon,
  Add as AddIcon,
  Close as CloseIcon,
  Description as DescriptionIcon,
  TableChart as TableChartIcon,
  InfoOutlined as InfoIcon,
} from '@mui/icons-material';

import type { MCPSelectionConfig, MCPServerSelection } from '../../types/system.ts';
import { getAlertTypes, submitAlert, handleAPIError } from '../../services/api.ts';
import { sessionDetailPath } from '../../constants/routes.ts';
import { MCPSelection } from './MCPSelection.tsx';

// ────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────

interface KeyValuePair {
  id: string;
  key: string;
  value: string;
}

const generateId = () => Math.random().toString(36).substring(2, 11);

/**
 * Filter out servers with empty tool arrays from MCP selection config.
 * Backend treats tools: [] as invalid, so we remove those servers.
 */
function filterMCPSelection(
  config: MCPSelectionConfig | undefined,
): MCPSelectionConfig | undefined {
  if (!config) return undefined;

  const filteredServers: MCPServerSelection[] = config.servers.filter((server) => {
    return server.tools === null || server.tools === undefined ||
      (Array.isArray(server.tools) && server.tools.length > 0);
  });

  if (filteredServers.length === 0 && !config.native_tools) return undefined;

  return { ...config, servers: filteredServers };
}

// ────────────────────────────────────────────────────────────
// Resubmit state shape (passed via react-router location.state)
// ────────────────────────────────────────────────────────────

interface ResubmitState {
  resubmit: boolean;
  alertType?: string;
  runbook?: string | null;
  alertData?: string;
  sessionId?: string;
  mcpSelection?: MCPSelectionConfig | null;
}

// ────────────────────────────────────────────────────────────
// Component
// ────────────────────────────────────────────────────────────

export function ManualAlertForm() {
  const location = useLocation();
  const navigate = useNavigate();

  // Track if we've already processed resubmit state
  const resubmitProcessedRef = useRef(false);
  const defaultAlertTypeRef = useRef<string | null>(null);

  // Re-submission state
  const [sourceSessionId, setSourceSessionId] = useState<string | null>(null);
  const [showResubmitBanner, setShowResubmitBanner] = useState(false);

  // Common fields
  const [alertType, setAlertType] = useState('');
  const [runbookUrl, setRunbookUrl] = useState('');
  const [mcpSelection, setMcpSelection] = useState<MCPSelectionConfig | undefined>(undefined);

  // Mode selection (0 = Structured, 1 = Text) - Default to Text
  const [mode, setMode] = useState(1);

  // Mode A: Key-value pairs
  const [keyValuePairs, setKeyValuePairs] = useState<KeyValuePair[]>([
    { id: generateId(), key: 'cluster', value: '' },
    { id: generateId(), key: 'namespace', value: '' },
    { id: generateId(), key: 'message', value: '' },
  ]);

  // Mode B: Free text
  const [freeText, setFreeText] = useState('');

  // Available options
  const [availableAlertTypes, setAvailableAlertTypes] = useState<string[]>([]);
  const [defaultAlertType, setDefaultAlertType] = useState<string>('');

  // UI state
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);

  // ── STEP 1: Process resubmit state ────────────────────

  useEffect(() => {
    const state = location.state as ResubmitState | null;

    if (state?.resubmit && state?.alertData && !resubmitProcessedRef.current) {
      resubmitProcessedRef.current = true;

      if (state.alertType) {
        defaultAlertTypeRef.current = state.alertType;
      }

      setSourceSessionId(state.sessionId || null);
      setShowResubmitBanner(true);

      if (state.runbook) {
        setRunbookUrl(state.runbook);
      }

      if (state.mcpSelection) {
        setMcpSelection(state.mcpSelection);
      }

      // Always use text mode for re-submissions
      setMode(1);

      // Try to parse as JSON for cleaner display
      const alertData = state.alertData;
      try {
        const parsed = JSON.parse(alertData);
        // If it's { message: "text" }, extract just the text
        const keys = Object.keys(parsed);
        if (keys.length === 1 && keys[0] === 'message' && typeof parsed.message === 'string') {
          setFreeText(parsed.message);
        } else {
          setFreeText(JSON.stringify(parsed, null, 2));
        }
      } catch {
        // Not JSON — use as-is (raw text)
        setFreeText(alertData);
      }

      // Clear location state to prevent re-population on refresh
      navigate(location.pathname, { replace: true, state: {} });
    }
  }, [location, navigate]);

  // ── STEP 2: Load alert types ──────────────────────────

  useEffect(() => {
    const loadOptions = async () => {
      try {
        const resp = await getAlertTypes();
        if (resp && resp.alert_types) {
          const types = resp.alert_types.map((at) => at.type);

          // Use first alert type as default (or from resubmit)
          const resubmitDefault = defaultAlertTypeRef.current;
          let finalTypes = types;
          if (resubmitDefault && !types.includes(resubmitDefault)) {
            finalTypes = [resubmitDefault, ...types];
          }

          setAvailableAlertTypes(finalTypes);

          // Determine default: first type from API
          const apiDefault = types.length > 0 ? types[0] : '';
          setDefaultAlertType(apiDefault);

          if (resubmitDefault) {
            setAlertType(resubmitDefault);
          } else {
            setAlertType(apiDefault);
          }
        }
      } catch (err) {
        console.error('Failed to load alert types:', err);
        setError('Failed to load options from backend. Please check if the backend is running.');
      }
    };

    loadOptions();
  }, []);

  // ── Key-value pair helpers ────────────────────────────

  const addKeyValuePair = () => {
    setKeyValuePairs((prev) => [...prev, { id: generateId(), key: '', value: '' }]);
  };

  const removeKeyValuePair = (id: string) => {
    setKeyValuePairs((prev) => prev.filter((pair) => pair.id !== id));
  };

  const updateKeyValuePair = (id: string, field: 'key' | 'value', newValue: string) => {
    setKeyValuePairs((prev) =>
      prev.map((pair) => (pair.id === id ? { ...pair, [field]: newValue } : pair)),
    );
    if (error) setError(null);
    if (success) setSuccess(null);
  };

  // ── Submit handlers ───────────────────────────────────

  const handleSubmit = async () => {
    setError(null);
    setSuccess(null);
    setLoading(true);

    try {
      // Validate alert type
      if (!alertType || alertType.trim().length === 0) {
        setError('Alert Type is required');
        return;
      }

      // Build data string based on mode
      let data: string;

      if (mode === 1) {
        // Text mode
        if (!freeText || freeText.trim().length === 0) {
          setError('Alert data cannot be empty');
          return;
        }
        data = freeText;
      } else {
        // Structured mode — build JSON from key-value pairs
        const processedData: Record<string, string> = {};
        for (const pair of keyValuePairs) {
          if (!pair.key && !pair.value) continue;
          if (!pair.key || pair.key.trim().length === 0) {
            setError('Key cannot be empty if value is provided');
            return;
          }
          const trimmedKey = pair.key.trim();
          const trimmedValue = pair.value.trim();
          if (trimmedValue) {
            processedData[trimmedKey] = trimmedValue;
          }
        }
        if (Object.keys(processedData).length === 0) {
          setError('At least one key-value pair with a value is required');
          return;
        }
        data = JSON.stringify(processedData);
      }

      // Build request payload matching Go SubmitAlertRequest JSON tags
      const payload: { data: string; alert_type?: string; runbook?: string; mcp?: MCPSelectionConfig } = {
        data,
      };

      // Only include alert_type if it's different from the default
      if (alertType.trim() !== defaultAlertType) {
        payload.alert_type = alertType.trim();
      }

      // Add runbook only if provided
      if (runbookUrl.trim()) {
        payload.runbook = runbookUrl.trim();
      }

      // Add MCP selection if configured (only when user made changes from defaults)
      const filteredMCP = filterMCPSelection(mcpSelection);
      if (filteredMCP !== undefined) {
        payload.mcp = filteredMCP;
      }

      const response = await submitAlert(payload);

      // Navigate directly to session detail page
      navigate(sessionDetailPath(response.session_id));
    } catch (err: unknown) {
      console.error('Error submitting alert:', err);
      setError(handleAPIError(err));
    } finally {
      setLoading(false);
    }
  };

  // ── Render ────────────────────────────────────────────

  return (
    <Box sx={{ width: '100%' }}>
      {/* Header Section */}
      <Box sx={{ mb: 3, px: 3 }}>
        <Typography
          variant="h4"
          component="h1"
          gutterBottom
          sx={{ fontWeight: 600, mb: 1, letterSpacing: '-0.02em' }}
        >
          Submit Alert for Analysis
        </Typography>
        <Typography variant="body1" color="text.secondary" sx={{ fontSize: '1rem', lineHeight: 1.6 }}>
          Enter alert details as plain text or use structured key-value pairs.
        </Typography>
      </Box>

      {/* Re-submit banner */}
      {showResubmitBanner && sourceSessionId && (
        <Box sx={{ mb: 3, px: 3 }}>
          <MuiAlert
            severity="info"
            icon={<InfoIcon />}
            onClose={() => setShowResubmitBanner(false)}
            sx={{ borderRadius: 3, '& .MuiAlert-icon': { fontSize: 24 } }}
          >
            <Typography variant="body2">
              <strong>Pre-filled from previous session:</strong>{' '}
              <code
                style={{
                  backgroundColor: 'rgba(0, 0, 0, 0.05)',
                  padding: '2px 6px',
                  borderRadius: '4px',
                  fontFamily: 'monospace',
                  fontSize: '0.875rem',
                }}
              >
                {sourceSessionId.slice(-12)}
              </code>
            </Typography>
            <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 0.5 }}>
              You can modify any fields before submitting.
            </Typography>
          </MuiAlert>
        </Box>
      )}

      <Card
        elevation={0}
        sx={{
          borderRadius: 2,
          border: '1px solid',
          borderColor: 'divider',
          overflow: 'visible',
        }}
      >
        <CardContent sx={{ p: 0 }}>
          {/* Alert Messages */}
          {error && (
            <Box sx={{ px: 4, pt: 3 }}>
              <MuiAlert
                severity="error"
                sx={{ borderRadius: 3, '& .MuiAlert-icon': { fontSize: 24 } }}
              >
                <Typography
                  variant="body2"
                  component="pre"
                  sx={{ whiteSpace: 'pre-wrap', fontFamily: 'inherit' }}
                >
                  {error}
                </Typography>
              </MuiAlert>
            </Box>
          )}

          {success && (
            <Box sx={{ px: 4, pt: 3 }}>
              <MuiAlert
                severity="success"
                sx={{ borderRadius: 3, '& .MuiAlert-icon': { fontSize: 24 } }}
              >
                <Typography
                  variant="body2"
                  component="pre"
                  sx={{ whiteSpace: 'pre-wrap', fontFamily: 'inherit' }}
                >
                  {success}
                </Typography>
              </MuiAlert>
            </Box>
          )}

          {/* Configuration Section */}
          <Box sx={{ px: 4, pt: error || success ? 2 : 4, pb: 3 }}>
            <Typography
              variant="overline"
              sx={{
                color: 'text.secondary',
                fontWeight: 700,
                letterSpacing: 1.2,
                fontSize: '0.8rem',
                mb: 2,
                display: 'block',
              }}
            >
              Configuration
            </Typography>

            <Stack direction={{ xs: 'column', md: 'row' }} spacing={3}>
              <TextField
                select
                fullWidth
                label="Alert Type"
                value={alertType}
                onChange={(e) => setAlertType(e.target.value)}
                required
                helperText="The type of alert for agent selection"
                disabled={availableAlertTypes.length === 0}
                variant="filled"
                sx={{
                  '& .MuiFilledInput-root': {
                    borderRadius: 2,
                    '&:before, &:after': { display: 'none' },
                  },
                }}
              >
                {availableAlertTypes.length === 0 ? (
                  <MenuItem disabled>Loading alert types...</MenuItem>
                ) : (
                  availableAlertTypes.map((type) => (
                    <MenuItem key={type} value={type}>
                      {type}
                    </MenuItem>
                  ))
                )}
              </TextField>

              <TextField
                fullWidth
                label="Runbook URL"
                value={runbookUrl}
                onChange={(e) => setRunbookUrl(e.target.value)}
                placeholder="https://github.com/org/repo/blob/main/runbooks/..."
                helperText="Optional runbook URL for the agent to reference"
                disabled={loading}
                variant="filled"
                sx={{
                  '& .MuiFilledInput-root': {
                    borderRadius: 2,
                    '&:before, &:after': { display: 'none' },
                  },
                }}
              />
            </Stack>
          </Box>

          {/* MCP Server Configuration */}
          <MCPSelection
            value={mcpSelection}
            onChange={setMcpSelection}
            disabled={loading}
            alertType={alertType}
          />

          {/* Input Method Tabs */}
          <Box sx={{ px: 4, py: 2, bgcolor: 'rgba(25, 118, 210, 0.04)' }}>
            <Typography
              variant="overline"
              sx={{
                color: 'text.secondary',
                fontWeight: 700,
                letterSpacing: 1.2,
                fontSize: '0.8rem',
                mb: 2,
                display: 'block',
              }}
            >
              Input Method
            </Typography>
            <Box sx={{ display: 'flex', gap: 1, flexWrap: 'wrap' }}>
              {/* Text mode button */}
              <Box
                onClick={() => setMode(1)}
                sx={{
                  flex: { xs: '1 1 100%', sm: '0 1 auto' },
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  gap: 1.5,
                  py: 1.75,
                  px: 4,
                  borderRadius: 1,
                  cursor: 'pointer',
                  transition: 'all 0.2s cubic-bezier(0.4, 0, 0.2, 1)',
                  bgcolor: mode === 1 ? 'primary.main' : 'transparent',
                  color: mode === 1 ? 'primary.contrastText' : 'text.primary',
                  border: '2px solid',
                  borderColor: mode === 1 ? 'primary.main' : 'grey.300',
                  '&:hover': {
                    bgcolor: mode === 1 ? 'primary.dark' : 'action.hover',
                    borderColor: mode === 1 ? 'primary.dark' : 'grey.400',
                  },
                }}
              >
                <DescriptionIcon sx={{ fontSize: 22 }} />
                <Typography
                  variant="body1"
                  sx={{ fontWeight: mode === 1 ? 700 : 600, fontSize: '0.95rem' }}
                >
                  Text
                </Typography>
              </Box>

              {/* Structured mode button */}
              <Box
                onClick={() => setMode(0)}
                sx={{
                  flex: { xs: '1 1 100%', sm: '0 1 auto' },
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  gap: 1.5,
                  py: 1.75,
                  px: 4,
                  borderRadius: 1,
                  cursor: 'pointer',
                  transition: 'all 0.2s cubic-bezier(0.4, 0, 0.2, 1)',
                  bgcolor: mode === 0 ? 'primary.main' : 'transparent',
                  color: mode === 0 ? 'primary.contrastText' : 'text.primary',
                  border: '2px solid',
                  borderColor: mode === 0 ? 'primary.main' : 'grey.300',
                  '&:hover': {
                    bgcolor: mode === 0 ? 'primary.dark' : 'action.hover',
                    borderColor: mode === 0 ? 'primary.dark' : 'grey.400',
                  },
                }}
              >
                <TableChartIcon sx={{ fontSize: 22 }} />
                <Typography
                  variant="body1"
                  sx={{ fontWeight: mode === 0 ? 700 : 600, fontSize: '0.95rem' }}
                >
                  Structured Input
                </Typography>
              </Box>
            </Box>
          </Box>

          {/* ── Structured Input Form ────────────────────────── */}
          {mode === 0 && (
            <Box
              sx={{
                px: 4,
                py: 4,
                animation: 'fadeIn 0.3s ease-in-out',
                '@keyframes fadeIn': {
                  from: { opacity: 0, transform: 'translateY(8px)' },
                  to: { opacity: 1, transform: 'translateY(0)' },
                },
              }}
            >
              <Box
                sx={{
                  display: 'flex',
                  alignItems: 'flex-start',
                  justifyContent: 'space-between',
                  mb: 3,
                }}
              >
                <Box>
                  <Typography variant="h6" sx={{ fontWeight: 600, mb: 0.5, fontSize: '1.25rem' }}>
                    Alert Data
                  </Typography>
                  <Typography variant="body2" color="text.secondary">
                    Enter structured key-value pairs for your alert data
                  </Typography>
                </Box>
                <Button
                  startIcon={<AddIcon />}
                  onClick={addKeyValuePair}
                  variant="contained"
                  size="large"
                  sx={{
                    borderRadius: 1,
                    textTransform: 'none',
                    fontWeight: 600,
                    px: 3,
                    boxShadow: 1,
                    '&:hover': { boxShadow: 2 },
                  }}
                >
                  Add Field
                </Button>
              </Box>

              <Stack spacing={2}>
                {keyValuePairs.map((pair) => (
                  <Paper
                    key={pair.id}
                    elevation={0}
                    sx={{
                      display: 'flex',
                      alignItems: 'flex-start',
                      gap: 2,
                      p: 3,
                      borderRadius: 1,
                      border: '1px solid',
                      borderColor: 'divider',
                      bgcolor: 'grey.50',
                      transition: 'all 0.2s cubic-bezier(0.4, 0, 0.2, 1)',
                      '&:hover': {
                        borderColor: 'primary.light',
                        bgcolor: 'background.paper',
                        boxShadow: 1,
                      },
                    }}
                  >
                    <TextField
                      label="Key"
                      value={pair.key}
                      onChange={(e) => updateKeyValuePair(pair.id, 'key', e.target.value)}
                      placeholder="e.g., cluster, namespace"
                      variant="filled"
                      sx={{
                        flex: 1,
                        '& .MuiFilledInput-root': {
                          borderRadius: 2,
                          '&:before, &:after': { display: 'none' },
                        },
                      }}
                    />
                    <TextField
                      label="Value"
                      value={pair.value}
                      onChange={(e) => updateKeyValuePair(pair.id, 'value', e.target.value)}
                      placeholder="Field value"
                      variant="filled"
                      sx={{
                        flex: 2,
                        '& .MuiFilledInput-root': {
                          borderRadius: 2,
                          '&:before, &:after': { display: 'none' },
                        },
                      }}
                    />
                    <IconButton
                      onClick={() => removeKeyValuePair(pair.id)}
                      size="large"
                      sx={{ color: 'error.main', mt: 1, '&:hover': { bgcolor: 'error.lighter' } }}
                      title="Remove field"
                    >
                      <CloseIcon />
                    </IconButton>
                  </Paper>
                ))}
              </Stack>

              {/* Submit Button */}
              <Box sx={{ mt: 4, pt: 3, borderTop: '1px solid', borderColor: 'divider' }}>
                <Button
                  variant="contained"
                  size="large"
                  startIcon={
                    loading ? <CircularProgress size={22} color="inherit" /> : <SendIcon />
                  }
                  disabled={loading}
                  fullWidth
                  onClick={handleSubmit}
                  sx={{
                    py: 2,
                    borderRadius: 1,
                    fontSize: '1rem',
                    fontWeight: 600,
                    textTransform: 'none',
                    boxShadow: 2,
                    '&:hover': { boxShadow: 4 },
                  }}
                >
                  {loading ? 'Submitting Alert...' : 'Send Alert'}
                </Button>
              </Box>
            </Box>
          )}

          {/* ── Text Form ────────────────────────────────────── */}
          {mode === 1 && (
            <Box
              sx={{
                px: 4,
                py: 4,
                animation: 'fadeIn 0.3s ease-in-out',
                '@keyframes fadeIn': {
                  from: { opacity: 0, transform: 'translateY(8px)' },
                  to: { opacity: 1, transform: 'translateY(0)' },
                },
              }}
            >
              <Box sx={{ mb: 3 }}>
                <Typography variant="h6" sx={{ fontWeight: 600, mb: 0.5, fontSize: '1.25rem' }}>
                  Alert Data
                </Typography>
                <Typography variant="body2" color="text.secondary">
                  Paste or type your alert details. Text will be sent as-is to the agent.
                </Typography>
              </Box>

              <Paper
                elevation={0}
                sx={{
                  position: 'relative',
                  borderRadius: 1,
                  border: '1px solid',
                  borderColor: 'divider',
                  bgcolor: 'grey.50',
                  p: 0.5,
                  transition: 'all 0.2s cubic-bezier(0.4, 0, 0.2, 1)',
                  '&:hover': { borderColor: 'primary.light', bgcolor: 'background.paper' },
                  '&:focus-within': {
                    borderColor: 'primary.main',
                    bgcolor: 'background.paper',
                    boxShadow: 1,
                  },
                }}
              >
                <TextField
                  fullWidth
                  multiline
                  rows={16}
                  value={freeText}
                  onChange={(e) => {
                    setFreeText(e.target.value);
                    if (error) setError(null);
                    if (success) setSuccess(null);
                  }}
                  placeholder={`Alert: ProgressingApplication
Severity: warning
Environment: staging
Cluster: host
Namespace: openshift-gitops
Pod: openshift-gitops-application-controller-0
Message: The 'tarsy' Argo CD application is stuck in 'Progressing' status`}
                  variant="filled"
                  sx={{
                    '& .MuiFilledInput-root': {
                      bgcolor: 'transparent',
                      borderRadius: 2.5,
                      fontFamily: 'Consolas, Monaco, "Courier New", monospace',
                      fontSize: '0.9rem',
                      lineHeight: 1.6,
                      '&:before, &:after': { display: 'none' },
                      '&:hover': { bgcolor: 'transparent' },
                      '&.Mui-focused': { bgcolor: 'transparent' },
                    },
                    '& .MuiInputBase-input': {
                      fontFamily: 'Consolas, Monaco, "Courier New", monospace',
                      '&::placeholder': { opacity: 0.5 },
                    },
                  }}
                />
                {/* Character and line count */}
                {freeText && (
                  <Box
                    sx={{
                      display: 'flex',
                      justifyContent: 'flex-end',
                      alignItems: 'center',
                      mt: 0.5,
                      px: 1,
                    }}
                  >
                    <Typography variant="caption" color="text.secondary">
                      {freeText.length} characters, {freeText.split('\n').length} lines
                    </Typography>
                  </Box>
                )}
              </Paper>

              {/* Submit Button */}
              <Box sx={{ mt: 4, pt: 3, borderTop: '1px solid', borderColor: 'divider' }}>
                <Button
                  variant="contained"
                  size="large"
                  startIcon={
                    loading ? <CircularProgress size={22} color="inherit" /> : <SendIcon />
                  }
                  disabled={loading}
                  fullWidth
                  onClick={handleSubmit}
                  sx={{
                    py: 2,
                    borderRadius: 1,
                    fontSize: '1rem',
                    fontWeight: 600,
                    textTransform: 'none',
                    boxShadow: 2,
                    '&:hover': { boxShadow: 4 },
                  }}
                >
                  {loading ? 'Submitting Alert...' : 'Send Alert'}
                </Button>
              </Box>
            </Box>
          )}
        </CardContent>
      </Card>
    </Box>
  );
}
