import { useState, useMemo } from 'react';
import {
  Paper,
  Typography,
  Box,
  Chip,
  IconButton,
  Collapse,
  Button,
  alpha,
  Link,
} from '@mui/material';
import type { Theme } from '@mui/material/styles';
import { ExpandMore, OpenInNew, AccessTime } from '@mui/icons-material';
import ErrorBoundary from '../shared/ErrorBoundary';
import CopyButton from '../shared/CopyButton';
import JsonDisplay from '../shared/JsonDisplay';

interface OriginalAlertCardProps {
  /** Raw alert_data string from the session (JSON or plain text) */
  alertData: string;
  /** Session status — terminal sessions collapse the card by default */
  sessionStatus?: string;
}

/**
 * Severity → MUI chip color mapping
 */
function getSeverityColor(
  severity: string,
): 'default' | 'error' | 'warning' | 'info' | 'success' {
  switch (severity.toLowerCase()) {
    case 'critical':
      return 'error';
    case 'high':
      return 'warning';
    case 'medium':
      return 'info';
    case 'low':
      return 'success';
    default:
      return 'default';
  }
}

/**
 * Environment → MUI chip color mapping
 */
function getEnvironmentColor(
  env: string,
): 'default' | 'error' | 'warning' | 'info' | 'success' {
  switch (env.toLowerCase()) {
    case 'production':
    case 'prod':
      return 'error';
    case 'staging':
    case 'stage':
      return 'warning';
    case 'development':
    case 'dev':
      return 'info';
    default:
      return 'info';
  }
}

/**
 * Format a field key to human-readable form: "alert_type" → "Alert Type"
 */
function formatKeyName(key: string): string {
  return key.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase());
}

const FIELD_PRIORITY: Record<string, number> = {
  cluster: 1, environment: 1, severity: 1,
  alert_type: 2, namespace: 2,
  workload_name: 3, workload_cid: 3, node: 3,
  received_at: 4, created_at: 4, started_at: 4,
  user_email: 5, user_sgroup: 5, user_group: 5,
};

function fieldPriority(key: string): number {
  return FIELD_PRIORITY[key.toLowerCase()] ?? 99;
}

/**
 * Returns true if a value renders as a short, single-line display
 * (suitable for 2-column grid layout).
 */
function isSimpleValue(value: unknown): boolean {
  if (value === null || value === undefined) return true;
  if (typeof value === 'number' || typeof value === 'boolean') return true;
  if (typeof value === 'string') {
    return !value.includes('\n') && value.length < 120 &&
      !value.startsWith('http://') && !value.startsWith('https://') &&
      !/^\d{4}-\d{2}-\d{2}T/.test(value);
  }
  return false;
}

const TERMINAL_STATUSES = new Set(['completed', 'failed', 'cancelled', 'timed_out']);

/**
 * Render a single alert field value based on its type.
 */
function FieldValue({ fieldKey, value }: { fieldKey: string; value: unknown }) {
  const [isJsonExpanded, setIsJsonExpanded] = useState(false);
  const [isTextExpanded, setIsTextExpanded] = useState(false);

  if (value === null || value === undefined) {
    return (
      <Typography variant="body2" color="text.secondary" sx={{ fontStyle: 'italic' }}>
        —
      </Typography>
    );
  }

  // URLs — with special runbook styling
  if (
    typeof value === 'string' &&
    (value.startsWith('http://') || value.startsWith('https://'))
  ) {
    const isRunbook = fieldKey === 'runbook' || fieldKey === 'runbook_url';
    return (
      <Link
        href={value}
        target="_blank"
        rel="noopener noreferrer"
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: 1,
          bgcolor: isRunbook
            ? (theme: Theme) => alpha(theme.palette.info.main, 0.05)
            : 'action.hover',
          color: isRunbook ? 'info.main' : 'inherit',
          p: 1.5,
          borderRadius: 1,
          fontFamily: 'monospace',
          fontSize: '0.875rem',
          textDecoration: 'none',
          wordBreak: 'break-word',
          '&:hover': {
            bgcolor: isRunbook
              ? (theme: Theme) => alpha(theme.palette.info.main, 0.1)
              : 'action.selected',
            textDecoration: 'underline',
          },
        }}
      >
        <OpenInNew fontSize="small" />
        {value}
      </Link>
    );
  }

  // Timestamps (ISO date strings)
  if (typeof value === 'string' && /^\d{4}-\d{2}-\d{2}T/.test(value)) {
    return (
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
        <AccessTime fontSize="small" sx={{ color: 'text.secondary' }} />
        <Typography
          variant="body2"
          sx={{
            fontFamily: 'monospace',
            fontSize: '0.875rem',
            bgcolor: 'action.hover',
            px: 1.5,
            py: 0.5,
            borderRadius: 1,
          }}
        >
          {value}
        </Typography>
      </Box>
    );
  }

  // Objects / arrays — render with interactive JSON viewer
  if (typeof value === 'object') {
    const isLong = JSON.stringify(value, null, 2).split('\n').length > 8;
    return (
      <Box>
        {isLong && (
          <Button
            size="small"
            variant="text"
            onClick={() => setIsJsonExpanded(!isJsonExpanded)}
            sx={{ mb: 0.5, textTransform: 'none', fontSize: '0.75rem' }}
          >
            {isJsonExpanded ? 'Hide JSON' : 'Show JSON'}
          </Button>
        )}
        <Collapse in={!isLong || isJsonExpanded} timeout={300}>
          <JsonDisplay data={value} maxHeight={300} />
        </Collapse>
      </Box>
    );
  }

  // Multi-line strings with expand/collapse
  if (typeof value === 'string' && value.includes('\n')) {
    const lines = value.split('\n');
    const isLong = lines.length > 10;
    const lineCount = lines.length;
    return (
      <Box sx={{ position: 'relative' }}>
        <Collapse in={!isLong || isTextExpanded} collapsedSize={isLong ? 150 : undefined} timeout={300}>
          <Typography
            component="pre"
            sx={{
              bgcolor: 'action.hover',
              p: 1.5,
              borderRadius: 1,
              fontFamily: 'monospace',
              fontSize: '0.825rem',
              lineHeight: 1.6,
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-word',
              overflowX: 'auto',
              maxHeight: isTextExpanded ? 500 : undefined,
              overflowY: 'auto',
              border: '1px solid',
              borderColor: 'divider',
            }}
          >
            {value}
          </Typography>
        </Collapse>
        {isLong && !isTextExpanded && (
          <Box
            sx={(theme) => ({
              position: 'absolute',
              bottom: 0,
              left: 0,
              right: 0,
              height: 60,
              background: 'linear-gradient(transparent, rgba(255,255,255,0.95))',
              display: 'flex',
              alignItems: 'flex-end',
              justifyContent: 'center',
              pb: 0.5,
              ...theme.applyStyles('dark', {
                background: `linear-gradient(transparent, ${theme.palette.background.paper})`,
              }),
            })}
          >
            <Button
              size="small"
              variant="text"
              onClick={() => setIsTextExpanded(true)}
              sx={{ textTransform: 'none', fontSize: '0.75rem' }}
            >
              Expand ({lineCount} lines)
            </Button>
          </Box>
        )}
        {isLong && isTextExpanded && (
          <Button
            size="small"
            variant="text"
            onClick={() => setIsTextExpanded(false)}
            sx={{ mt: 0.5, textTransform: 'none', fontSize: '0.75rem' }}
          >
            Collapse
          </Button>
        )}
      </Box>
    );
  }

  // Simple values
  return (
    <Typography
      variant="body2"
      sx={{
        fontFamily:
          fieldKey.includes('id') || fieldKey.includes('hash') ? 'monospace' : 'inherit',
        fontSize: '0.875rem',
        bgcolor: 'action.hover',
        px: 1,
        py: 0.5,
        borderRadius: 0.5,
        wordBreak: 'break-word',
      }}
    >
      {String(value)}
    </Typography>
  );
}

/**
 * AlertDataContent — the rich field rendering for alert data.
 * Parses JSON, sorts fields by priority, renders in a 2-column grid
 * with type-aware formatting. Exported for reuse in SessionHeader.
 */
export function AlertDataContent({ alertData }: { alertData: string }) {
  const parsed = useMemo(() => {
    try {
      return JSON.parse(alertData);
    } catch {
      return null;
    }
  }, [alertData]);

  const isObject = parsed && typeof parsed === 'object' && !Array.isArray(parsed);

  const fields = useMemo(() => {
    if (!isObject) return [];
    return Object.entries(parsed).sort(([a], [b]) => {
      const pa = fieldPriority(a);
      const pb = fieldPriority(b);
      return pa !== pb ? pa - pb : a.localeCompare(b);
    });
  }, [parsed, isObject]);

  const headlineKeys = new Set(['severity', 'environment', 'alert_type']);
  const simpleFields = useMemo(() => fields.filter(([k, v]) => isSimpleValue(v) && !headlineKeys.has(k)), [fields]);
  const complexFields = useMemo(() => fields.filter(([k, v]) => !isSimpleValue(v) && !headlineKeys.has(k)), [fields]);

  const severity = isObject ? parsed.severity : null;
  const environment = isObject ? parsed.environment : null;
  const alertType = isObject ? parsed.alert_type : null;

  return (
    <ErrorBoundary componentName="AlertDataContent">
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
        {(severity || environment || alertType) && (
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 2, flexWrap: 'wrap' }}>
            {severity && (
              <Chip
                label={String(severity).toUpperCase()}
                color={getSeverityColor(String(severity))}
                size="small"
                sx={{ fontWeight: 600 }}
              />
            )}
            {environment && (
              <Chip
                label={String(environment).toUpperCase()}
                color={getEnvironmentColor(String(environment))}
                size="small"
                variant="outlined"
              />
            )}
            {alertType && (
              <Typography variant="body2" color="text.secondary">
                {String(alertType)}
              </Typography>
            )}
          </Box>
        )}

        {isObject ? (
          <>
            {simpleFields.length > 0 && (
              <Box
                sx={{
                  display: 'grid',
                  gridTemplateColumns: { xs: '1fr', sm: '1fr 1fr' },
                  gap: 2,
                }}
              >
                {simpleFields.map(([key, value]) => (
                  <ErrorBoundary
                    key={key}
                    componentName={`Field: ${key}`}
                    fallback={
                      <Box sx={(theme) => ({ p: 1, bgcolor: alpha(theme.palette.error.main, 0.05), border: '1px solid', borderColor: alpha(theme.palette.error.main, 0.2), borderRadius: 1 })}>
                        <Typography variant="caption" color="error">Error rendering field &quot;{key}&quot;</Typography>
                      </Box>
                    }
                  >
                    <Box>
                      <Typography variant="subtitle2" color="text.secondary" gutterBottom>
                        {formatKeyName(key)}
                      </Typography>
                      <FieldValue fieldKey={key} value={value} />
                    </Box>
                  </ErrorBoundary>
                ))}
              </Box>
            )}

            {complexFields.map(([key, value]) => (
              <ErrorBoundary
                key={key}
                componentName={`Field: ${key}`}
                fallback={
                  <Box sx={(theme) => ({ p: 1, bgcolor: alpha(theme.palette.error.main, 0.05), border: '1px solid', borderColor: alpha(theme.palette.error.main, 0.2), borderRadius: 1 })}>
                    <Typography variant="caption" color="error">Error rendering field &quot;{key}&quot;: {String(value)}</Typography>
                  </Box>
                }
              >
                <Box>
                  <Typography variant="subtitle2" color="text.secondary" gutterBottom>
                    {formatKeyName(key)}
                  </Typography>
                  <FieldValue fieldKey={key} value={value} />
                </Box>
              </ErrorBoundary>
            ))}
          </>
        ) : (
          <Typography
            component="pre"
            sx={{
              bgcolor: 'action.hover',
              p: 2,
              borderRadius: 1,
              fontFamily: 'monospace',
              fontSize: '0.825rem',
              lineHeight: 1.6,
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-word',
              overflowX: 'auto',
              maxHeight: 500,
              overflowY: 'auto',
            }}
          >
            {alertData}
          </Typography>
        )}
      </Box>
    </ErrorBoundary>
  );
}

/**
 * OriginalAlertCard — collapsible card displaying the original alert data.
 * Collapsed by default for terminal sessions. Shows a summary preview when collapsed.
 */
export default function OriginalAlertCard({ alertData, sessionStatus }: OriginalAlertCardProps) {
  const isTerminal = TERMINAL_STATUSES.has(sessionStatus ?? '');
  const [isExpanded, setIsExpanded] = useState(!isTerminal);

  const parsed = useMemo(() => {
    try {
      const p = JSON.parse(alertData);
      return p && typeof p === 'object' && !Array.isArray(p) ? p : null;
    } catch {
      return null;
    }
  }, [alertData]);

  const fieldCount = parsed ? Object.keys(parsed).length : 0;

  const summaryText = useMemo(() => {
    if (parsed) {
      return Object.entries(parsed)
        .slice(0, 3)
        .map(([key, value]) => `${formatKeyName(key)}: ${String(value)}`)
        .join('  ·  ');
    }
    return alertData.split('\n')[0]?.trim() || null;
  }, [parsed, alertData]);

  return (
    <Paper sx={{ p: 3 }}>
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          cursor: 'pointer',
          mb: isExpanded ? 2 : 0,
        }}
        onClick={() => setIsExpanded(!isExpanded)}
      >
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.5 }}>
          <Typography variant="h6" sx={{ fontWeight: 600 }}>
            Alert Data
          </Typography>
          {parsed && (
            <Typography variant="caption" color="text.secondary">
              {fieldCount} {fieldCount === 1 ? 'field' : 'fields'}
            </Typography>
          )}
        </Box>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
          <Box onClick={(e) => e.stopPropagation()}>
            <CopyButton text={alertData} variant="icon" size="small" tooltip="Copy raw alert data" />
          </Box>
          <IconButton
            size="small"
            aria-label={isExpanded ? 'Collapse alert data' : 'Expand alert data'}
            sx={{
              transition: 'transform 0.4s',
              transform: isExpanded ? 'rotate(180deg)' : 'rotate(0deg)',
            }}
          >
            <ExpandMore />
          </IconButton>
        </Box>
      </Box>

      {!isExpanded && summaryText && (
        <Typography
          variant="body2"
          color="text.secondary"
          sx={{ mt: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
        >
          {summaryText}
        </Typography>
      )}

      <Collapse in={isExpanded} timeout={400}>
        <AlertDataContent alertData={alertData} />
      </Collapse>
    </Paper>
  );
}
