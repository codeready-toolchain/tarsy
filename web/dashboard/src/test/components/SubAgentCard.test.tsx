import { render, screen } from '@testing-library/react';
import { ThemeProvider, createTheme } from '@mui/material/styles';
import SubAgentCard from '../../components/timeline/SubAgentCard';
import { FLOW_ITEM, type FlowItem } from '../../utils/timelineParser';
import type { ExecutionOverview } from '../../types/session';
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

function cancelledOverview(errorMessage: string): ExecutionOverview {
  return {
    execution_id: 'sub-1',
    agent_name: 'LogAnalyzer',
    agent_index: 1,
    status: EXECUTION_STATUS.CANCELLED,
    llm_backend: 'test',
    llm_provider: 'test',
    started_at: '2025-01-15T10:00:00Z',
    completed_at: '2025-01-15T10:01:00Z',
    duration_ms: 1000,
    error_message: errorMessage,
    input_tokens: 0,
    output_tokens: 0,
    total_tokens: 0,
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

  it('opens a status-only card for a persisted overview reason', () => {
    render(
      <ThemeProvider theme={theme}>
        <SubAgentCard
          items={[]}
          executionOverview={cancelledOverview('Cancelled by SREOrchestrator: too slow')}
        />
      </ThemeProvider>,
    );

    expect(screen.getByTestId('ExpandLessIcon')).toBeInTheDocument();
    expect(screen.getByText(/Cancelled by SREOrchestrator: too slow/)).toBeInTheDocument();
  });

  it('expands when a persisted reason arrives after mount', () => {
    const { rerender } = render(
      <ThemeProvider theme={theme}>
        <SubAgentCard
          items={[]}
          executionStatus={{
            status: EXECUTION_STATUS.STARTED,
            stageId: 'stage-1',
            agentIndex: 1,
          }}
        />
      </ThemeProvider>,
    );
    expect(screen.getByTestId('ExpandMoreIcon')).toBeInTheDocument();

    rerender(
      <ThemeProvider theme={theme}>
        <SubAgentCard
          items={[]}
          executionOverview={cancelledOverview('Cancelled by SREOrchestrator: duplicate')}
        />
      </ThemeProvider>,
    );

    expect(screen.getByTestId('ExpandLessIcon')).toBeInTheDocument();
    expect(screen.getByText(/Cancelled by SREOrchestrator: duplicate/)).toBeInTheDocument();
  });

  it('keeps a cancelled card with timeline items collapsed', () => {
    render(
      <ThemeProvider theme={theme}>
        <SubAgentCard
          items={[makeItem()]}
          executionOverview={cancelledOverview('Cancelled by SREOrchestrator: too slow')}
        />
      </ThemeProvider>,
    );

    expect(screen.getByTestId('ExpandMoreIcon')).toBeInTheDocument();
  });
});
