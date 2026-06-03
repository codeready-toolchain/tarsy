import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { EditFeedbackModal } from '../../components/dashboard/EditFeedbackModal';
import { QUALITY_RATING, REVIEW_SELECTION } from '../../types/api';

vi.mock('../../config/env.ts', () => ({
  config: { isDevelopment: false, isProduction: true },
  DASHBOARD_VERSION: 'test',
  urls: {
    api: { base: '', health: '/health' },
    websocket: { base: 'ws://localhost:8080', path: '/api/v1/ws' },
    oauth: { signIn: '/oauth2/sign_in', signOut: '/oauth2/sign_out', userInfo: '/oauth2/userinfo' },
  },
}));

const defaultProps = {
  open: true,
  onClose: vi.fn(),
  onSave: vi.fn(),
  initialQualityRating: QUALITY_RATING.ACCURATE,
  initialActionTaken: 'Fixed the issue',
  initialInvestigationFeedback: 'Good analysis',
};

describe('EditFeedbackModal', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders all four radio options including Acknowledge', () => {
    render(<EditFeedbackModal {...defaultProps} />);

    expect(screen.getByText('Accurate')).toBeInTheDocument();
    expect(screen.getByText('Partially Accurate')).toBeInTheDocument();
    expect(screen.getByText('Inaccurate')).toBeInTheDocument();
    expect(screen.getByText('Acknowledge')).toBeInTheDocument();
  });

  it('shows downgrade warning when switching from quality rating to Acknowledge', async () => {
    const user = userEvent.setup();
    render(<EditFeedbackModal {...defaultProps} />);

    const ackRadio = screen.getByRole('radio', { name: /acknowledge/i });
    await user.click(ackRadio);

    expect(screen.getByText(/switching to acknowledge will remove the current quality rating/i)).toBeInTheDocument();
  });

  it('does not show downgrade warning when initial rating is empty', async () => {
    const user = userEvent.setup();
    render(<EditFeedbackModal {...defaultProps} initialQualityRating="" />);

    const ackRadio = screen.getByRole('radio', { name: /acknowledge/i });
    await user.click(ackRadio);

    expect(screen.queryByText(/switching to acknowledge will remove the current quality rating/i)).not.toBeInTheDocument();
  });

  it('hides feedback fields when Acknowledge is selected', async () => {
    const user = userEvent.setup();
    render(<EditFeedbackModal {...defaultProps} />);

    const ackRadio = screen.getByRole('radio', { name: /acknowledge/i });
    await user.click(ackRadio);

    const actionField = screen.getByLabelText(/action taken/i);
    const collapseWrapper = actionField.closest('.MuiCollapse-root');
    await waitFor(() => {
      expect(collapseWrapper).toHaveStyle({ height: '0px' });
    });
  });

  it('shows "Acknowledge" button when Acknowledge is selected', async () => {
    const user = userEvent.setup();
    render(<EditFeedbackModal {...defaultProps} />);

    const ackRadio = screen.getByRole('radio', { name: /acknowledge/i });
    await user.click(ackRadio);

    expect(screen.getByRole('button', { name: 'Acknowledge' })).toBeInTheDocument();
  });

  it('shows "Save Changes" button for quality ratings', () => {
    render(<EditFeedbackModal {...defaultProps} initialQualityRating={QUALITY_RATING.INACCURATE} />);

    const inaccurateRadio = screen.getByRole('radio', { name: /inaccurate/i });
    expect(inaccurateRadio).toBeChecked();

    expect(screen.getByRole('button', { name: 'Save Changes' })).toBeInTheDocument();
  });

  it('calls onSave with acknowledge value when submitted', async () => {
    const user = userEvent.setup();
    const onSave = vi.fn();
    render(<EditFeedbackModal {...defaultProps} onSave={onSave} />);

    const ackRadio = screen.getByRole('radio', { name: /acknowledge/i });
    await user.click(ackRadio);

    const submitBtn = screen.getByRole('button', { name: 'Acknowledge' });
    await user.click(submitBtn);

    expect(onSave).toHaveBeenCalledWith(REVIEW_SELECTION.ACKNOWLEDGE, 'Fixed the issue', 'Good analysis');
  });
});
