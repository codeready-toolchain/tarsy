/**
 * Sticky banner shown when a new dashboard version is available.
 *
 * Reads dashboardVersionChanged from VersionContext. Cannot be dismissed —
 * user must refresh to get updated JS bundles.
 */

import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import Box from '@mui/material/Box';
import { keyframes } from '@mui/material/styles';
import useMediaQuery from '@mui/material/useMediaQuery';
import { Refresh as RefreshIcon, Warning as WarningIcon } from '@mui/icons-material';
import { useVersion } from '../../contexts/VersionContext.tsx';

const pulseAnimation = keyframes`
  0% { opacity: 1; }
  50% { opacity: 0.85; }
  100% { opacity: 1; }
`;

export function VersionUpdateBanner() {
  const { dashboardVersionChanged } = useVersion();
  const prefersReducedMotion = useMediaQuery('(prefers-reduced-motion: reduce)');

  if (!dashboardVersionChanged) {
    return null;
  }

  const handleRefresh = () => {
    window.location.reload();
  };

  return (
    <Box
      sx={{
        position: 'sticky',
        top: 0,
        zIndex: (theme) => theme.zIndex.appBar + 1,
        width: '100%',
        animation: prefersReducedMotion ? 'none' : `${pulseAnimation} 2s ease-in-out infinite`,
      }}
    >
      <Alert
        severity="warning"
        icon={<WarningIcon sx={{ fontSize: 28 }} />}
        action={
          <Button
            variant="contained"
            color="warning"
            size="medium"
            startIcon={<RefreshIcon />}
            onClick={handleRefresh}
            sx={{ fontWeight: 'bold' }}
          >
            Refresh Now
          </Button>
        }
        sx={{
          borderRadius: 0,
          fontSize: '1.15rem',
          py: 2.5,
          px: 3,
          '& .MuiAlert-message': {
            display: 'flex',
            alignItems: 'center',
            width: '100%',
            fontSize: '1.15rem',
          },
          '& .MuiAlert-action': {
            alignItems: 'center',
            pt: 0,
          },
        }}
      >
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.5 }}>
          <Box sx={{ fontWeight: 'bold', fontSize: '1.3rem' }}>
            New Dashboard Version Available!
          </Box>
          <Box sx={{ fontSize: '1.05rem' }}>Refresh now to get the latest updates.</Box>
        </Box>
      </Alert>
    </Box>
  );
}
