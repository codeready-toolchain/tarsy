/**
 * Dismissible banner for system warnings (MCP server issues, etc.).
 *
 * Polls /api/v1/system/warnings every 10 seconds. Each warning is displayed
 * as an expandable Alert. Fetch errors are silently ignored — warnings are
 * non-critical.
 */

import { useEffect, useState } from 'react';
import Alert from '@mui/material/Alert';
import AlertTitle from '@mui/material/AlertTitle';
import Box from '@mui/material/Box';
import Collapse from '@mui/material/Collapse';
import IconButton from '@mui/material/IconButton';
import { ExpandMore as ExpandMoreIcon } from '@mui/icons-material';
import { getSystemWarnings } from '../../services/api.ts';
import type { SystemWarning } from '../../types/system.ts';

const POLL_INTERVAL_MS = 10_000; // 10 seconds

export function SystemWarningBanner() {
  const [warnings, setWarnings] = useState<SystemWarning[]>([]);
  const [expandedId, setExpandedId] = useState<string | null>(null);

  useEffect(() => {
    const fetchWarnings = async () => {
      try {
        const data = await getSystemWarnings();
        setWarnings(data.warnings);
      } catch {
        // Don't show error to user — warnings are non-critical
      }
    };

    fetchWarnings();
    const interval = setInterval(fetchWarnings, POLL_INTERVAL_MS);
    return () => clearInterval(interval);
  }, []);

  if (warnings.length === 0) {
    return null;
  }

  const handleToggleExpand = (warningId: string) => {
    setExpandedId(expandedId === warningId ? null : warningId);
  };

  return (
    <Box sx={{ mb: 2 }}>
      {warnings.map((warning) => (
        <Alert
          key={warning.id}
          severity="warning"
          sx={{ mb: 1 }}
          action={
            warning.details ? (
              <IconButton
                size="small"
                onClick={() => handleToggleExpand(warning.id)}
                aria-label={
                  expandedId === warning.id ? 'Collapse details' : 'Expand details'
                }
              >
                <ExpandMoreIcon
                  sx={{
                    transform:
                      expandedId === warning.id ? 'rotate(180deg)' : 'rotate(0deg)',
                    transition: 'transform 0.3s',
                  }}
                />
              </IconButton>
            ) : undefined
          }
        >
          <AlertTitle>System Warning</AlertTitle>
          {warning.message}
          {warning.details && (
            <Collapse in={expandedId === warning.id}>
              <Box sx={{ mt: 1, fontSize: '0.875rem', opacity: 0.9 }}>
                {warning.details}
              </Box>
            </Collapse>
          )}
        </Alert>
      ))}
    </Box>
  );
}
