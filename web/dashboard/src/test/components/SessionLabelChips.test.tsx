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

  it('leaves unknown labels uncolored', () => {
    render(<SessionLabelChips labels={['noise', 'false_positive']} />);
    expect(screen.getByText('noise').closest('.MuiChip-root')).toHaveClass('MuiChip-colorDefault');
    expect(screen.getByText('false_positive').closest('.MuiChip-root')).toHaveClass('MuiChip-colorDefault');
  });
});
