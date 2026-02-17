/**
 * System Status page — MCP server health and tools overview.
 *
 * Layout follows SubmitAlertPage pattern: SharedHeader + content + VersionFooter.
 */

import Box from '@mui/material/Box';
import Container from '@mui/material/Container';

import { SharedHeader } from '../components/layout/SharedHeader.tsx';
import { VersionFooter } from '../components/layout/VersionFooter.tsx';
import { FloatingSubmitAlertFab } from '../components/common/FloatingSubmitAlertFab.tsx';
import { MCPServerStatusView } from '../components/system/MCPServerStatusView.tsx';

export function SystemStatusPage() {
  return (
    <Box sx={{ minHeight: '100vh', backgroundColor: 'background.default', px: 2, py: 2 }}>
      <SharedHeader title="System Status" showBackButton />

      <Container maxWidth={false} sx={{ py: 4, px: { xs: 1, sm: 2 } }}>
        <MCPServerStatusView />
        <VersionFooter />
      </Container>

      <FloatingSubmitAlertFab />
    </Box>
  );
}
