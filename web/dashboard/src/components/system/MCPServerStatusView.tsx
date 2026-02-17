/**
 * MCP Server Status View — displays health, tools, and error details
 * for all configured MCP servers.
 *
 * New component (no old TARSy equivalent). Uses GET /api/v1/system/mcp-servers.
 */

import { useState, useEffect, useCallback } from 'react';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Chip from '@mui/material/Chip';
import CircularProgress from '@mui/material/CircularProgress';
import Collapse from '@mui/material/Collapse';
import IconButton from '@mui/material/IconButton';
import Paper from '@mui/material/Paper';
import Table from '@mui/material/Table';
import TableBody from '@mui/material/TableBody';
import TableCell from '@mui/material/TableCell';
import TableRow from '@mui/material/TableRow';
import Tooltip from '@mui/material/Tooltip';
import Typography from '@mui/material/Typography';
import {
  ExpandMore as ExpandMoreIcon,
  Refresh as RefreshIcon,
} from '@mui/icons-material';
import { getMCPServers } from '../../services/api.ts';
import { timeAgo } from '../../utils/format.ts';
import type { MCPServerStatus } from '../../types/system.ts';

const POLL_INTERVAL_MS = 15_000; // 15 seconds, matching backend health check interval

export function MCPServerStatusView() {
  const [servers, setServers] = useState<MCPServerStatus[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [expandedServer, setExpandedServer] = useState<string | null>(null);

  const fetchServers = useCallback(async () => {
    try {
      const data = await getMCPServers();
      setServers(data.servers);
      setError(null);
    } catch (err) {
      setError('Failed to load MCP server status');
      console.error('Failed to fetch MCP servers:', err);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchServers();
    const interval = setInterval(fetchServers, POLL_INTERVAL_MS);
    return () => clearInterval(interval);
  }, [fetchServers]);

  const handleToggleExpand = (serverId: string) => {
    setExpandedServer(expandedServer === serverId ? null : serverId);
  };

  const handleRefresh = () => {
    setLoading(true);
    fetchServers();
  };

  if (loading && servers.length === 0) {
    return (
      <Box sx={{ display: 'flex', justifyContent: 'center', py: 6 }}>
        <CircularProgress />
      </Box>
    );
  }

  if (error && servers.length === 0) {
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

  return (
    <Box>
      {/* Header row */}
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: 2 }}>
        <Typography variant="h6">MCP Servers</Typography>
        <Tooltip title="Refresh">
          <IconButton onClick={handleRefresh} size="small">
            <RefreshIcon />
          </IconButton>
        </Tooltip>
      </Box>

      {servers.length === 0 && (
        <Paper sx={{ p: 4, textAlign: 'center' }}>
          <Typography color="text.secondary">No MCP servers configured</Typography>
        </Paper>
      )}

      {servers.map((server) => (
        <Paper key={server.id} sx={{ mb: 2, overflow: 'hidden' }}>
          {/* Server header */}
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
              px: 2,
              py: 1.5,
              cursor: server.tools.length > 0 ? 'pointer' : 'default',
            }}
            onClick={() => {
              if (server.tools.length > 0) handleToggleExpand(server.id);
            }}
          >
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.5 }}>
              <Chip
                label={server.healthy ? 'Healthy' : 'Unhealthy'}
                color={server.healthy ? 'success' : 'error'}
                size="small"
              />
              <Typography variant="subtitle1" sx={{ fontWeight: 600 }}>
                {server.id}
              </Typography>
              <Chip label={`${server.tool_count} tools`} size="small" variant="outlined" />
            </Box>

            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
              <Typography variant="body2" color="text.secondary">
                {timeAgo(server.last_check)}
              </Typography>
              {server.tools.length > 0 && (
                <ExpandMoreIcon
                  sx={{
                    transform: expandedServer === server.id ? 'rotate(180deg)' : 'rotate(0deg)',
                    transition: 'transform 0.3s',
                    color: 'text.secondary',
                  }}
                />
              )}
            </Box>
          </Box>

          {/* Error display */}
          {server.error && (
            <Alert severity="error" sx={{ mx: 2, mb: 1.5, borderRadius: 1 }}>
              {server.error}
            </Alert>
          )}

          {/* Expandable tool list */}
          {server.tools.length > 0 && (
            <Collapse in={expandedServer === server.id}>
              <Box sx={{ px: 2, pb: 2 }}>
                <Table size="small">
                  <TableBody>
                    {server.tools.map((tool) => (
                      <TableRow key={tool.name}>
                        <TableCell
                          sx={{ fontFamily: 'monospace', fontWeight: 500, whiteSpace: 'nowrap' }}
                        >
                          {tool.name}
                        </TableCell>
                        <TableCell sx={{ color: 'text.secondary' }}>
                          {tool.description || '—'}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </Box>
            </Collapse>
          )}
        </Paper>
      ))}
    </Box>
  );
}
