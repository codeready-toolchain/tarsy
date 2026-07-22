/**
 * System Status page — MCP health and effective configuration.
 *
 * Layout follows SubmitAlertPage pattern: SharedHeader + content + VersionFooter.
 * Tabs: MCP Health (polled) | Configuration (fetch once).
 */

import { useState } from 'react';
import Box from '@mui/material/Box';
import Container from '@mui/material/Container';
import Tab from '@mui/material/Tab';
import Tabs from '@mui/material/Tabs';

import { SharedHeader } from '../components/layout/SharedHeader.tsx';
import { VersionFooter } from '../components/layout/VersionFooter.tsx';
import { FloatingSubmitAlertFab } from '../components/common/FloatingSubmitAlertFab.tsx';
import { MCPServerStatusView } from '../components/system/MCPServerStatusView.tsx';
import { ConfigViewer } from '../components/system/ConfigViewer.tsx';

type SystemTab = 'mcp' | 'config';

export function SystemStatusPage() {
  const [tab, setTab] = useState<SystemTab>('mcp');

  return (
    <Box sx={{ minHeight: '100vh', backgroundColor: 'background.default', px: 2, py: 2 }}>
      <SharedHeader title="System Status" showBackButton />

      <Container maxWidth={false} sx={{ py: 4, px: { xs: 1, sm: 2 } }}>
        <Tabs
          value={tab}
          onChange={(_, value: SystemTab) => setTab(value)}
          aria-label="System status tabs"
          sx={{ mb: 3, borderBottom: 1, borderColor: 'divider' }}
        >
          <Tab label="MCP Health" value="mcp" id="system-tab-mcp" aria-controls="system-tabpanel-mcp" />
          <Tab
            label="Configuration"
            value="config"
            id="system-tab-config"
            aria-controls="system-tabpanel-config"
          />
        </Tabs>

        <Box
          role="tabpanel"
          hidden={tab !== 'mcp'}
          id="system-tabpanel-mcp"
          aria-labelledby="system-tab-mcp"
        >
          <MCPServerStatusView pollingEnabled={tab === 'mcp'} />
        </Box>

        <Box
          role="tabpanel"
          hidden={tab !== 'config'}
          id="system-tabpanel-config"
          aria-labelledby="system-tab-config"
        >
          <ConfigViewer />
        </Box>

        <VersionFooter />
      </Container>

      <FloatingSubmitAlertFab />
    </Box>
  );
}
