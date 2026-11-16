/**
 * Post-investigation stages run after the session is terminal (like scoring),
 * so the processing indicator must stay visible while one is still running.
 */

import { render, screen } from '@testing-library/react';
import { ThemeProvider, createTheme } from '@mui/material/styles';
import ConversationTimeline from '../../components/session/ConversationTimeline';
import type { StageOverview } from '../../types/session';
import { EXECUTION_STATUS } from '../../constants/sessionStatus';
import { STAGE_TYPE } from '../../constants/eventTypes';

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

function makeStage(id: string, stageType: string, status: string, index: number): StageOverview {
  return {
    id,
    stage_name: id,
    stage_index: index,
    stage_type: stageType,
    status,
    parallel_type: null,
    expected_agent_count: 1,
    started_at: '2025-01-15T10:00:00Z',
    completed_at: null,
    executions: [],
  };
}

function renderTimeline(postInvestigationStatus: string) {
  render(
    <ThemeProvider theme={theme}>
      <ConversationTimeline
        items={[]}
        stages={[
          makeStage('investigation', STAGE_TYPE.INVESTIGATION, EXECUTION_STATUS.COMPLETED, 0),
          makeStage('Reflection', STAGE_TYPE.SCORING, EXECUTION_STATUS.COMPLETED, 1),
          makeStage('file-ticket', STAGE_TYPE.POST_INVESTIGATION, postInvestigationStatus, 2),
        ]}
        isActive={false}
        sessionId="session-pi"
      />
    </ThemeProvider>,
  );
}

describe('ConversationTimeline post-investigation processing indicator', () => {
  it('shows the indicator while a post-investigation stage is running on a terminal session', () => {
    renderTimeline(EXECUTION_STATUS.ACTIVE);
    expect(screen.getByText('Processing...')).toBeInTheDocument();
  });

  it('hides the indicator once the post-investigation stage is terminal', () => {
    renderTimeline(EXECUTION_STATUS.COMPLETED);
    expect(screen.queryByText('Processing...')).not.toBeInTheDocument();
  });
});
