import Box from '@mui/material/Box';
import Typography from '@mui/material/Typography';
import { DASHBOARD_VERSION } from '../../config/env.ts';

/**
 * Footer displaying dashboard version.
 * Backend version comparison, tooltip, and mismatch display wired in Phase 7.7.
 *
 * Layout matches old TARSy footer: centered text, divider top, "Powered by AI" branding.
 */
export function VersionFooter() {
  // Phase 7.7: VersionContext will provide backendVersion and backendStatus
  // for separate version display, tooltip, and loading/unavailable states.
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
      <Typography variant="body2" color="text.secondary">
        TARSy - Powered by AI &bull; Dashboard: {DASHBOARD_VERSION}
      </Typography>
    </Box>
  );
}
