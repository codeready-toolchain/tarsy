/**
 * Footer displaying version information with live backend version updates.
 *
 * Shows single "Version: X" when dashboard and agent match, separate versions
 * when they differ, and loading/unavailable states. Tooltip shows agent status.
 */

import Box from '@mui/material/Box';
import Typography from '@mui/material/Typography';
import Tooltip from '@mui/material/Tooltip';
import { DASHBOARD_VERSION } from '../../config/env.ts';
import { useVersion } from '../../contexts/VersionContext.tsx';

export function VersionFooter() {
  const { backendVersion: agentVersion, backendStatus } = useVersion();

  const showSingleVersion = agentVersion && agentVersion === DASHBOARD_VERSION;
  const showSeparateVersions =
    agentVersion && agentVersion !== DASHBOARD_VERSION && agentVersion !== 'unavailable';

  return (
    <Box
      component="footer"
      sx={{
        mt: 4,
        mb: 2,
        py: 2,
        textAlign: 'center',
        borderTop: '1px solid',
        borderColor: 'divider',
      }}
    >
      {showSingleVersion && (
        <Tooltip title={`Agent status: ${backendStatus}`} arrow>
          <Typography variant="body2" color="text.secondary" sx={{ cursor: 'help' }}>
            TARSy - Powered by AI &bull; Version: {DASHBOARD_VERSION}
          </Typography>
        </Tooltip>
      )}

      {showSeparateVersions && (
        <Tooltip title={`Agent status: ${backendStatus}`} arrow>
          <Typography variant="body2" color="text.secondary" sx={{ cursor: 'help' }}>
            TARSy - Powered by AI &bull; Dashboard: {DASHBOARD_VERSION} &bull; Agent:{' '}
            {agentVersion}
          </Typography>
        </Tooltip>
      )}

      {!agentVersion && backendStatus === 'checking' && (
        <Typography variant="body2" color="text.secondary">
          TARSy - Powered by AI &bull; Loading version info...
        </Typography>
      )}

      {agentVersion === 'unavailable' && (
        <Typography variant="body2" color="text.secondary">
          TARSy - Powered by AI &bull; Dashboard: {DASHBOARD_VERSION} &bull; Agent: unavailable
        </Typography>
      )}

      {/* Fallback when backendStatus is 'error' and no agentVersion set yet */}
      {!agentVersion && backendStatus === 'error' && (
        <Typography variant="body2" color="text.secondary">
          TARSy - Powered by AI &bull; Dashboard: {DASHBOARD_VERSION}
        </Typography>
      )}
    </Box>
  );
}
