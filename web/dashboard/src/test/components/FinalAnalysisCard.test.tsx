import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import FinalAnalysisCard from '../../components/session/FinalAnalysisCard';
import { SESSION_STATUS } from '../../constants/sessionStatus';

vi.mock('../../config/env.ts', () => ({
  config: { isDevelopment: false, isProduction: true },
  DASHBOARD_VERSION: 'test',
  urls: {
    api: { base: '', health: '/health' },
    websocket: { base: 'ws://localhost:8080', path: '/api/v1/ws' },
    oauth: { signIn: '/oauth2/sign_in', signOut: '/oauth2/sign_out', userInfo: '/oauth2/userinfo' },
  },
}));

describe('FinalAnalysisCard cancel attribution', () => {
  it('renders user-provided reason as plain text, not markdown', () => {
    render(
      <MemoryRouter>
        <FinalAnalysisCard
          analysis={null}
          summary={null}
          sessionStatus={SESSION_STATUS.CANCELLED}
          errorMessage={null}
          cancelledBy="alice@example.com"
          cancelReason="[click](https://evil.example)"
          expandCounter={1}
        />
      </MemoryRouter>,
    );

    expect(
      screen.getByText('Cancelled by alice@example.com: [click](https://evil.example)'),
    ).toBeInTheDocument();
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
  });
});
