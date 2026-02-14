import Box from '@mui/material/Box';
import Typography from '@mui/material/Typography';
import { DASHBOARD_VERSION } from '../../config/env.ts';

/**
 * Footer displaying dashboard version.
 * Backend version comparison and mismatch display wired in Phase 7.7.
 */
export function VersionFooter() {
  return (
    <Box
      component="footer"
      sx={{
        py: 1,
        px: 2,
        mt: 'auto',
        textAlign: 'center',
        borderTop: '1px solid',
        borderColor: 'divider',
        backgroundColor: 'background.default',
      }}
    >
      <Typography variant="caption" color="text.secondary">
        TARSy Dashboard v{DASHBOARD_VERSION}
      </Typography>
    </Box>
  );
}
