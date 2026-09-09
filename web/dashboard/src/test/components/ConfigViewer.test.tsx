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
  extra?: Partial<SystemConfigResponse>,
): SystemConfigResponse {
  return {
    defaults: null,
    queue: null,
    system: {
      allowed_ws_origins: [],
      prompt_caching: { enabled: promptCachingEnabled },
    },
    fallback_lists: {},
    label_maps: {},
    agents: {},
    chains: {},
    mcp_servers: {},
    llm_providers: {},
    skills: {},
    ...extra,
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

describe('ConfigViewer fallback lists', () => {
  it('shows a catalog name under Fallback lists', async () => {
    mockGetSystemConfig.mockResolvedValue(
      makeConfig(true, {
        fallback_lists: {
          premium: [{ llm_provider: 'claude-opus', llm_backend: 'langchain' }],
        },
      }),
    );
    const user = userEvent.setup();
    render(<ConfigViewer />);

    await user.click(await screen.findByText('Fallback lists'));
    expect(await screen.findByText('premium')).toBeInTheDocument();
  });
});

describe('ConfigViewer label maps', () => {
  it('shows a catalog name under Label maps', async () => {
    mockGetSystemConfig.mockResolvedValue(
      makeConfig(true, {
        label_maps: {
          builtin: {
            multi: false,
            instructions: 'Apply at most one label.',
            labels: [{ label: 'watch', description: 'Look again if it persists.' }],
          },
        },
      }),
    );
    const user = userEvent.setup();
    render(<ConfigViewer />);

    await user.click(await screen.findByText('Label maps'));
    expect(await screen.findByText('builtin')).toBeInTheDocument();
  });
});
