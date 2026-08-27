import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { SystemConfigResponse } from '../../types/system';

vi.mock('../../services/api.ts', () => ({
  getSystemConfig: vi.fn(),
  getSystemConfigSkill: vi.fn(),
  handleAPIError: (err: unknown) =>
    err instanceof Error ? err.message : 'An unexpected error occurred',
}));

import { getSystemConfig } from '../../services/api';
import { ConfigViewer } from '../../components/system/ConfigViewer';

const mockGetSystemConfig = vi.mocked(getSystemConfig);

function makeConfig(
  promptCachingEnabled: boolean,
): SystemConfigResponse {
  return {
    defaults: null,
    queue: null,
    system: {
      allowed_ws_origins: [],
      prompt_caching: { enabled: promptCachingEnabled },
    },
    agents: {},
    chains: {},
    mcp_servers: {},
    llm_providers: {},
    skills: {},
  };
}

describe('ConfigViewer prompt caching', () => {
  it('extracts prompt_caching.enabled from System', async () => {
    mockGetSystemConfig.mockResolvedValue(makeConfig(true));
    const user = userEvent.setup();
    render(<ConfigViewer />);

    await user.click(await screen.findByText('System'));
    expect(await screen.findByText('Prompt caching')).toBeInTheDocument();
    expect(screen.getByText('true')).toBeInTheDocument();
  });

  it('shows enabled false when the kill switch is off', async () => {
    mockGetSystemConfig.mockResolvedValue(makeConfig(false));
    const user = userEvent.setup();
    render(<ConfigViewer />);

    await user.click(await screen.findByText('System'));
    expect(await screen.findByText('Prompt caching')).toBeInTheDocument();
    expect(screen.getByText('false')).toBeInTheDocument();
  });
});
