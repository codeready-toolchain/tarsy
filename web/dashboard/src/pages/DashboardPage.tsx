import { Container } from '@mui/material';
import { SharedHeader } from '../components/layout/SharedHeader.tsx';
import { VersionFooter } from '../components/layout/VersionFooter.tsx';
import { DashboardView } from '../components/dashboard/DashboardView.tsx';

/**
 * Dashboard page — main session list view with active alerts, filters, and history.
 */
export function DashboardPage() {
  return (
    <>
      <SharedHeader title="TARSy Dashboard" />
      <Container maxWidth={false} sx={{ px: 2 }}>
        <DashboardView />
      </Container>
      <VersionFooter />
    </>
  );
}
