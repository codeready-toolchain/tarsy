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
});
