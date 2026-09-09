import { render, screen } from '@testing-library/react';
import { SessionLabelChips } from '../../components/common/SessionLabelChips';

describe('SessionLabelChips', () => {
  it('renders nothing for null', () => {
    const { container } = render(<SessionLabelChips labels={null} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('renders nothing for an empty array', () => {
    const { container } = render(<SessionLabelChips labels={[]} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('renders a chip per label', () => {
    render(<SessionLabelChips labels={['page', 'watch']} />);
    expect(screen.getByText('page')).toBeInTheDocument();
    expect(screen.getByText('watch')).toBeInTheDocument();
  });

  it('uses error color for urgent labels', () => {
    render(<SessionLabelChips labels={['action', 'Ban', 'page']} />);
    expect(screen.getByText('action').closest('.MuiChip-root')).toHaveClass('MuiChip-colorError');
    expect(screen.getByText('Ban').closest('.MuiChip-root')).toHaveClass('MuiChip-colorError');
    expect(screen.getByText('page').closest('.MuiChip-root')).toHaveClass('MuiChip-colorError');
  });

  it('uses info color for watch-style labels', () => {
    render(<SessionLabelChips labels={['watch', 'Monitor']} />);
    expect(screen.getByText('watch').closest('.MuiChip-root')).toHaveClass('MuiChip-colorInfo');
    expect(screen.getByText('Monitor').closest('.MuiChip-root')).toHaveClass('MuiChip-colorInfo');
  });

  it('mutes closable labels', () => {
    render(<SessionLabelChips labels={['noise', 'fp', 'False_Positive']} />);
    expect(screen.getByText('noise').closest('.MuiChip-root')).toHaveAttribute('data-muted', 'true');
    expect(screen.getByText('fp').closest('.MuiChip-root')).toHaveAttribute('data-muted', 'true');
    expect(screen.getByText('False_Positive').closest('.MuiChip-root')).toHaveAttribute('data-muted', 'true');
  });

  it('leaves unknown labels uncolored', () => {
    render(<SessionLabelChips labels={['ops-tag']} />);
    const chip = screen.getByText('ops-tag').closest('.MuiChip-root');
    expect(chip).toHaveClass('MuiChip-colorDefault');
    expect(chip).not.toHaveAttribute('data-muted');
  });
});
