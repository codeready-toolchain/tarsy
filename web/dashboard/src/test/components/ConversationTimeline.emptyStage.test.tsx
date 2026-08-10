/**
 * Empty stages must mount StageSeparator (data-stage-id) when deep-linked
 * so scroll resolution can find the stageSeparator target.
 */

import { render, waitFor } from '@testing-library/react';
import { ThemeProvider, createTheme } from '@mui/material/styles';
import ConversationTimeline from '../../components/session/ConversationTimeline';
import type { StageOverview } from '../../types/session';
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

function makeEmptyStage(id: string): StageOverview {
  return {
    id,
    stage_name: 'Empty Investigation',
    stage_index: 0,
    stage_type: 'investigation',
    status: EXECUTION_STATUS.COMPLETED,
    parallel_type: null,
    expected_agent_count: 1,
    started_at: '2025-01-15T10:00:00Z',
    completed_at: '2025-01-15T10:05:00Z',
    executions: [],
  };
}

describe('ConversationTimeline empty-stage deep link', () => {
  it('mounts StageSeparator for a terminal session with one empty stage', async () => {
    const stageId = 'stage-empty';

    render(
      <ThemeProvider theme={theme}>
        <ConversationTimeline
          items={[]}
          stages={[makeEmptyStage(stageId)]}
          isActive={false}
          defaultCollapsed
          sessionId="session-empty"
          focusRequest={{
            stageId,
            nonce: 1,
            scrollTarget: { kind: 'stageSeparator', stageId },
          }}
        />
      </ThemeProvider>,
    );

    await waitFor(() => {
      expect(document.querySelector(`[data-stage-id="${stageId}"]`)).not.toBeNull();
    });
  });
});
