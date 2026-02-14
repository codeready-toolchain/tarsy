import { useState, useMemo } from 'react';
import { Paper, Typography, Box, Chip, IconButton, Collapse, alpha, Link } from '@mui/material';
import { ExpandMore, OpenInNew, AccessTime } from '@mui/icons-material';
import ErrorBoundary from '../shared/ErrorBoundary';

interface OriginalAlertCardProps {
  /** Raw alert_data string from the session (JSON or plain text) */
  alertData: string;
}

/**
 * Severity → MUI chip color mapping
 */
function getSeverityColor(severity: string): 'default' | 'error' | 'warning' | 'info' | 'success' {
  switch (severity.toLowerCase()) {
    case 'critical': return 'error';
    case 'high': return 'warning';
    case 'medium': return 'info';
    case 'low': return 'success';
    default: return 'default';
  }
}

/**
 * Format a field key to human-readable form: "alert_type" → "Alert Type"
 */
function formatKeyName(key: string): string {
  return key
    .replace(/_/g, ' ')
    .replace(/\b\w/g, c => c.toUpperCase());
}

/**
 * Render a single alert field value based on its type.
 */
function FieldValue({ fieldKey, value }: { fieldKey: string; value: unknown }) {
  if (value === null || value === undefined) {
    return <Typography variant="body2" color="text.secondary" sx={{ fontStyle: 'italic' }}>—</Typography>;
  }

  // URLs
  if (typeof value === 'string' && (value.startsWith('http://') || value.startsWith('https://'))) {
    return (
      <Link
        href={value} target="_blank" rel="noopener noreferrer"
        sx={{ display: 'flex', alignItems: 'center', gap: 1, bgcolor: 'grey.50', p: 1.5, borderRadius: 1, fontFamily: 'monospace', fontSize: '0.875rem', textDecoration: 'none', wordBreak: 'break-word', '&:hover': { bgcolor: 'grey.100', textDecoration: 'underline' } }}
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
        <Typography variant="body2" sx={{ fontFamily: 'monospace', fontSize: '0.875rem', bgcolor: 'grey.50', px: 1.5, py: 0.5, borderRadius: 1 }}>
          {value}
        </Typography>
      </Box>
    );
  }

  // Objects / arrays — render as formatted JSON
  if (typeof value === 'object') {
    const formatted = JSON.stringify(value, null, 2);
    return (
      <Typography component="pre" sx={{ bgcolor: 'grey.50', p: 2, borderRadius: 1, fontFamily: 'monospace', fontSize: '0.825rem', lineHeight: 1.6, overflowX: 'auto', maxHeight: 300, overflowY: 'auto', whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
        {formatted}
      </Typography>
    );
  }

  // Multi-line strings
  if (typeof value === 'string' && value.includes('\n')) {
    return (
      <Typography component="pre" sx={{ bgcolor: 'grey.50', p: 1.5, borderRadius: 1, fontFamily: 'monospace', fontSize: '0.825rem', lineHeight: 1.6, whiteSpace: 'pre-wrap', wordBreak: 'break-word', overflowX: 'auto', maxHeight: 500, overflowY: 'auto', border: '1px solid', borderColor: 'grey.300' }}>
        {value}
      </Typography>
    );
  }

  // Simple values
  return (
    <Typography variant="body2" sx={{ fontFamily: fieldKey.includes('id') || fieldKey.includes('hash') ? 'monospace' : 'inherit', fontSize: '0.875rem', bgcolor: 'grey.50', px: 1, py: 0.5, borderRadius: 0.5, wordBreak: 'break-word' }}>
      {String(value)}
    </Typography>
  );
}

/**
 * OriginalAlertCard — collapsible card displaying the original alert data.
 * Parses JSON alert data and renders fields dynamically with type-aware formatting.
 * Wrapped in ErrorBoundary for resilience against malformed data.
 */
export default function OriginalAlertCard({ alertData }: OriginalAlertCardProps) {
  const [isExpanded, setIsExpanded] = useState(true);

  // Parse alert data (JSON string → object, or keep as string)
  const parsed = useMemo(() => {
    try {
      return JSON.parse(alertData);
    } catch {
      return null;
    }
  }, [alertData]);

  const isObject = parsed && typeof parsed === 'object' && !Array.isArray(parsed);
  const fields = isObject ? Object.entries(parsed).sort(([a], [b]) => a.localeCompare(b)) : [];

  // Extract special fields for header chips
  const severity = isObject ? parsed.severity : null;
  const environment = isObject ? parsed.environment : null;
  const alertType = isObject ? parsed.alert_type : null;

  return (
    <Paper sx={{ p: 3 }}>
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: 2 }}>
        <Typography variant="h6" sx={{ fontWeight: 600 }}>Original Alert Data</Typography>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          {isObject && (
            <Typography variant="caption" color="text.secondary">
              {fields.length} {fields.length === 1 ? 'field' : 'fields'}
            </Typography>
          )}
          <IconButton
            size="small" onClick={() => setIsExpanded(!isExpanded)}
            sx={{ transition: 'transform 0.4s', transform: isExpanded ? 'rotate(180deg)' : 'rotate(0deg)' }}
          >
            <ExpandMore />
          </IconButton>
        </Box>
      </Box>

      <Collapse in={isExpanded} timeout={400}>
        <ErrorBoundary componentName="OriginalAlertCard">
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
            {/* Header chips */}
            {(severity || environment || alertType) && (
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 2, flexWrap: 'wrap' }}>
                {severity && (
                  <Chip label={String(severity).toUpperCase()} color={getSeverityColor(String(severity))} size="small" sx={{ fontWeight: 600 }} />
                )}
                {environment && (
                  <Chip label={String(environment).toUpperCase()} color="info" size="small" variant="outlined" />
                )}
                {alertType && (
                  <Typography variant="body2" color="text.secondary">{String(alertType)}</Typography>
                )}
              </Box>
            )}

            {/* Dynamic fields */}
            {isObject ? (
              fields.map(([key, value]) => (
                <ErrorBoundary key={key} componentName={`Field: ${key}`} fallback={
                  <Box sx={(theme) => ({ p: 1, bgcolor: alpha(theme.palette.error.main, 0.05), border: '1px solid', borderColor: alpha(theme.palette.error.main, 0.2), borderRadius: 1 })}>
                    <Typography variant="caption" color="error">Error rendering field &quot;{key}&quot;</Typography>
                  </Box>
                }>
                  <Box>
                    <Typography variant="subtitle2" color="text.secondary" gutterBottom>
                      {formatKeyName(key)}
                    </Typography>
                    <FieldValue fieldKey={key} value={value} />
                  </Box>
                </ErrorBoundary>
              ))
            ) : (
              // Raw text fallback
              <Typography component="pre" sx={{ bgcolor: 'grey.50', p: 2, borderRadius: 1, fontFamily: 'monospace', fontSize: '0.825rem', lineHeight: 1.6, whiteSpace: 'pre-wrap', wordBreak: 'break-word', overflowX: 'auto', maxHeight: 500, overflowY: 'auto' }}>
                {alertData}
              </Typography>
            )}
          </Box>
        </ErrorBoundary>
      </Collapse>
    </Paper>
  );
}
