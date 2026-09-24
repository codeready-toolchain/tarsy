import { render, screen } from '@testing-library/react';
import { ScoreBadge } from '../../components/common/ScoreBadge';

describe('ScoreBadge', () => {
  it('treats scoring cancelled as a failed eval', () => {
    render(<ScoreBadge scoringStatus="cancelled" />);
    expect(screen.getByText('Failed')).toBeInTheDocument();
  });

  it('shows re-score when scoring cancelled and onClick is provided', () => {
    render(<ScoreBadge scoringStatus="cancelled" onClick={() => undefined} />);
    expect(screen.getByText('Re-score')).toBeInTheDocument();
  });

  it('shows a dash when scoring status is missing', () => {
    render(<ScoreBadge />);
    expect(screen.getByText('—')).toBeInTheDocument();
  });
});
