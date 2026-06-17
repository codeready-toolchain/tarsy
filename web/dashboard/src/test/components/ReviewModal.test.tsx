import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ReviewModal } from '../../components/dashboard/ReviewModal';
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

const baseProps = {
  open: true,
  onClose: vi.fn(),
  onSubmit: vi.fn(),
};

describe('ReviewModal', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe('complete mode', () => {
    const completeProps = { ...baseProps, mode: 'complete' as const };

    it('renders all four radio options', () => {
      render(<ReviewModal {...completeProps} />);

      expect(screen.getByText('Accurate')).toBeInTheDocument();
      expect(screen.getByText('Partially Accurate')).toBeInTheDocument();
      expect(screen.getByText('Inaccurate')).toBeInTheDocument();
      expect(screen.getByText('Acknowledge')).toBeInTheDocument();
    });

    it('shows feedback field and action taken toggle', () => {
      render(<ReviewModal {...completeProps} />);

      expect(screen.getByLabelText(/investigation feedback/i)).toBeInTheDocument();
      expect(screen.getByText(/add action taken note/i)).toBeInTheDocument();
    });

    it('reveals action taken field when toggle is clicked', async () => {
      const user = userEvent.setup();
      render(<ReviewModal {...completeProps} />);

      await user.click(screen.getByText(/add action taken note/i));
      expect(screen.getByLabelText(/action taken/i)).toBeInTheDocument();
    });

    it('hides feedback fields when Acknowledge is selected', async () => {
      const user = userEvent.setup();
      render(<ReviewModal {...completeProps} />);

      await user.click(screen.getByRole('radio', { name: /acknowledge/i }));

      const feedbackField = screen.getByLabelText(/investigation feedback/i);
      const collapseWrapper = feedbackField.closest('.MuiCollapse-root');
      await waitFor(() => {
        expect(collapseWrapper).toHaveStyle({ height: '0px' });
      });
    });

    it('shows "Complete Review" button for quality ratings', () => {
      render(<ReviewModal {...completeProps} />);

      expect(screen.getByRole('button', { name: 'Complete Review' })).toBeInTheDocument();
    });

    it('shows "Acknowledge" button when Acknowledge is selected', async () => {
      const user = userEvent.setup();
      render(<ReviewModal {...completeProps} />);

      await user.click(screen.getByRole('radio', { name: /acknowledge/i }));

      expect(screen.getByRole('button', { name: 'Acknowledge' })).toBeInTheDocument();
      expect(screen.queryByRole('button', { name: 'Complete Review' })).not.toBeInTheDocument();
    });

    it('calls onSubmit with acknowledge value', async () => {
      const user = userEvent.setup();
      const onSubmit = vi.fn();
      render(<ReviewModal {...completeProps} onSubmit={onSubmit} />);

      await user.click(screen.getByRole('radio', { name: /acknowledge/i }));
      await user.click(screen.getByRole('button', { name: 'Acknowledge' }));

      expect(onSubmit).toHaveBeenCalledWith(REVIEW_SELECTION.ACKNOWLEDGE, '', '');
    });

    it('calls onSubmit with quality rating and action taken', async () => {
      const user = userEvent.setup();
      const onSubmit = vi.fn();
      render(<ReviewModal {...completeProps} onSubmit={onSubmit} />);

      await user.click(screen.getByText(/add action taken note/i));
      await user.type(screen.getByLabelText(/action taken/i), 'Restarted the pod');
      await user.click(screen.getByRole('button', { name: 'Complete Review' }));

      expect(onSubmit).toHaveBeenCalledWith(QUALITY_RATING.ACCURATE, 'Restarted the pod', '');
    });

    it('pre-selects acknowledge when initialRating is acknowledge', () => {
      render(<ReviewModal {...completeProps} initialRating={REVIEW_SELECTION.ACKNOWLEDGE} />);

      expect(screen.getByRole('radio', { name: /acknowledge/i })).toBeChecked();
      expect(screen.getByRole('button', { name: 'Acknowledge' })).toBeInTheDocument();
    });
  });

  describe('edit mode', () => {
    const editProps = {
      ...baseProps,
      mode: 'edit' as const,
      initialRating: QUALITY_RATING.ACCURATE,
      initialActionTaken: 'Fixed the issue',
      initialInvestigationFeedback: 'Good analysis',
    };

    it('renders all four radio options', () => {
      render(<ReviewModal {...editProps} />);

      expect(screen.getByText('Accurate')).toBeInTheDocument();
      expect(screen.getByText('Partially Accurate')).toBeInTheDocument();
      expect(screen.getByText('Inaccurate')).toBeInTheDocument();
      expect(screen.getByText('Acknowledge')).toBeInTheDocument();
    });

    it('auto-expands action taken when pre-filled', () => {
      render(<ReviewModal {...editProps} />);

      expect(screen.getByLabelText(/action taken/i)).toBeInTheDocument();
      expect(screen.getByText(/hide action taken/i)).toBeInTheDocument();
    });

    it('collapses action taken when no initial value', () => {
      render(<ReviewModal {...editProps} initialActionTaken="" />);

      expect(screen.getByText(/add action taken note/i)).toBeInTheDocument();
    });

    it('shows downgrade warning when switching to Acknowledge', async () => {
      const user = userEvent.setup();
      render(<ReviewModal {...editProps} />);

      await user.click(screen.getByRole('radio', { name: /acknowledge/i }));

      expect(screen.getByText(/switching to acknowledge will remove the current quality rating/i)).toBeInTheDocument();
    });

    it('does not show downgrade warning when initial rating is empty', async () => {
      const user = userEvent.setup();
      render(<ReviewModal {...editProps} initialRating="" />);

      await user.click(screen.getByRole('radio', { name: /acknowledge/i }));

      expect(screen.queryByText(/switching to acknowledge will remove the current quality rating/i)).not.toBeInTheDocument();
    });

    it('hides feedback fields when Acknowledge is selected', async () => {
      const user = userEvent.setup();
      render(<ReviewModal {...editProps} />);

      await user.click(screen.getByRole('radio', { name: /acknowledge/i }));

      const feedbackField = screen.getByLabelText(/investigation feedback/i);
      const collapseWrapper = feedbackField.closest('.MuiCollapse-root');
      await waitFor(() => {
        expect(collapseWrapper).toHaveStyle({ height: '0px' });
      });
    });

    it('shows "Save Changes" button for quality ratings', () => {
      render(<ReviewModal {...editProps} />);

      expect(screen.getByRole('button', { name: 'Save Changes' })).toBeInTheDocument();
    });

    it('shows "Acknowledge" button when Acknowledge is selected', async () => {
      const user = userEvent.setup();
      render(<ReviewModal {...editProps} />);

      await user.click(screen.getByRole('radio', { name: /acknowledge/i }));

      expect(screen.getByRole('button', { name: 'Acknowledge' })).toBeInTheDocument();
    });

    it('calls onSubmit with acknowledge value', async () => {
      const user = userEvent.setup();
      const onSubmit = vi.fn();
      render(<ReviewModal {...editProps} onSubmit={onSubmit} />);

      await user.click(screen.getByRole('radio', { name: /acknowledge/i }));
      await user.click(screen.getByRole('button', { name: 'Acknowledge' }));

      expect(onSubmit).toHaveBeenCalledWith(REVIEW_SELECTION.ACKNOWLEDGE, '', '');
    });

    it('disables save button until values change', () => {
      render(<ReviewModal {...editProps} />);

      expect(screen.getByRole('button', { name: 'Save Changes' })).toBeDisabled();
    });

    it('enables save button when values change', async () => {
      const user = userEvent.setup();
      render(<ReviewModal {...editProps} />);

      await user.type(screen.getByLabelText(/investigation feedback/i), ' updated');

      expect(screen.getByRole('button', { name: 'Save Changes' })).toBeEnabled();
    });
  });
});
