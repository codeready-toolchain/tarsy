import { render, screen } from '@testing-library/react';
import { ThemeProvider, createTheme } from '@mui/material/styles';
import SubAgentCard from '../../components/timeline/SubAgentCard';
import { FLOW_ITEM, type FlowItem } from '../../utils/timelineParser';
import { TIMELINE_STATUS } from '../../constants/eventTypes';
import { EXECUTION_STATUS } from '../../constants/sessionStatus';

vi.mock('../../config/env.ts', () => ({
  config: { isDevelopment: false, isProduction: true },
  DASHBOARD_VERSION: 'test',
  urls: {
    api: { base: '', health: '/health' },
    websocket: { base: 'ws://localhost:8080', path: '/api/v1/ws' },
    oauth: { signIn: '/oauth2/sign_in', signOut: '/oauth2/sign_out', userInfo: '/oauth2/userinfo' },
  },
}));

const theme = createTheme();

function makeItem(): FlowItem {
  return {
    id: 'ev-1',
    type: FLOW_ITEM.THINKING,
    stageId: 'stage-1',
    executionId: 'sub-1',
    content: 'partial thought',
    status: TIMELINE_STATUS.COMPLETED,
    timestamp: '2025-01-15T10:00:00Z',
    sequenceNumber: 1,
  };
}

describe('SubAgentCard cancel attribution', () => {
  it('shows WS error_message on the cancelled banner without a REST overview', () => {
    render(
      <ThemeProvider theme={theme}>
        <SubAgentCard
          items={[makeItem()]}
          executionStatus={{
            status: EXECUTION_STATUS.CANCELLED,
            stageId: 'stage-1',
            agentIndex: 1,
            errorMessage: 'Cancelled by SREOrchestrator: too slow',
          }}
          fallbackAgentName="LogAnalyzer"
          forceExpandedItemId="ev-1"
        />
      </ThemeProvider>,
    );

    expect(screen.getByText(/Cancelled by SREOrchestrator: too slow/)).toBeInTheDocument();
  });
});
