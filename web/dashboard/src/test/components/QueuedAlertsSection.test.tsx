import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { QueuedAlertsSection } from '../../components/dashboard/QueuedAlertsSection';
import { cancelSession } from '../../services/api';
import { MAX_CANCEL_REASON_LENGTH } from '../../constants/sessionStatus';
import type { QueuedSessionItem } from '../../types/session';

vi.mock('../../config/env.ts', () => ({
  config: { isDevelopment: false, isProduction: true },
  DASHBOARD_VERSION: 'test',
  urls: {
    api: { base: '', health: '/health' },
    websocket: { base: 'ws://localhost:8080', path: '/api/v1/ws' },
    oauth: { signIn: '/oauth2/sign_in', signOut: '/oauth2/sign_out', userInfo: '/oauth2/userinfo' },
  },
}));

vi.mock('../../services/api.ts', () => ({
  cancelSession: vi.fn(),
  handleAPIError: vi.fn((error: unknown) => String(error)),
}));

const mockCancelSession = vi.mocked(cancelSession);

const queued: QueuedSessionItem = {
  id: 'queued-1',
  alert_type: 'NamespaceTerminating',
  chain_id: 'chain-1',
  status: 'pending',
  author: 'alice',
  created_at: '2025-01-15T10:00:00Z',
  queue_position: 1,
};

function renderQueued() {
  return render(
    <MemoryRouter>
      <QueuedAlertsSection sessions={[queued]} />
    </MemoryRouter>,
  );
}

describe('QueuedAlertsSection cancel dialog', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockCancelSession.mockResolvedValue({ session_id: 'queued-1', message: 'cancelled' });
  });

  it('submits without a reason when the field is empty', async () => {
    const user = userEvent.setup();
    renderQueued();

    await user.click(screen.getByText('Queued Alerts'));
    await user.click(screen.getByRole('button', { name: 'Cancel this queued session' }));
    await user.click(screen.getByRole('button', { name: 'Yes, Cancel Session' }));

    expect(mockCancelSession).toHaveBeenCalledWith('queued-1', '');
  });

  it('submits the typed reason', async () => {
    const user = userEvent.setup();
    renderQueued();

    await user.click(screen.getByText('Queued Alerts'));
    await user.click(screen.getByRole('button', { name: 'Cancel this queued session' }));
    await user.type(screen.getByLabelText(/reason \(optional\)/i), 'queued duplicate');
    await user.click(screen.getByRole('button', { name: 'Yes, Cancel Session' }));

    expect(mockCancelSession).toHaveBeenCalledWith('queued-1', 'queued duplicate');
  });

  it('disables confirm when the reason exceeds 500 runes', async () => {
    const user = userEvent.setup();
    renderQueued();

    await user.click(screen.getByText('Queued Alerts'));
    await user.click(screen.getByRole('button', { name: 'Cancel this queued session' }));
    const field = screen.getByLabelText(/reason \(optional\)/i);
    await user.click(field);
    await user.paste('a'.repeat(MAX_CANCEL_REASON_LENGTH + 1));

    expect(screen.getByRole('button', { name: 'Yes, Cancel Session' })).toBeDisabled();
    expect(mockCancelSession).not.toHaveBeenCalled();
  });
});
