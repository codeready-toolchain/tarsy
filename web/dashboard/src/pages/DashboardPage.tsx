import { Container, Typography } from '@mui/material';
import { SharedHeader } from '../components/layout/SharedHeader.tsx';
import { VersionFooter } from '../components/layout/VersionFooter.tsx';

/**
 * Dashboard page — main session list view.
 * Placeholder shell — content implemented in Phase 7.2.
 */
export function DashboardPage() {
  return (
    <>
      <SharedHeader title="TARSy Dashboard" />
      <Container maxWidth="xl">
        <Typography variant="h5" sx={{ mt: 2, mb: 2 }}>
          Dashboard
        </Typography>
        <Typography color="text.secondary">
          Session list, active alerts, and filters will be implemented in Phase 7.2.
        </Typography>
      </Container>
      <VersionFooter />
    </>
  );
}
