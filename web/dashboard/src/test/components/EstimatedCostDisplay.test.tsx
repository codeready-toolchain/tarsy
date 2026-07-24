import { render, screen } from '@testing-library/react';
import EstimatedCostDisplay from '../../components/shared/EstimatedCostDisplay';

describe('EstimatedCostDisplay', () => {
  it('renders nothing when disabled', () => {
    const { container } = render(
      <EstimatedCostDisplay
        enabled={false}
        estimatedCostUsd={1.23}
        costCompleteness="complete"
      />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('renders nothing when cost fields are absent', () => {
    const { container } = render(<EstimatedCostDisplay enabled />);
    expect(container).toBeEmptyDOMElement();
  });

  it('renders nothing when completeness is none', () => {
    const { container } = render(
      <EstimatedCostDisplay
        enabled
        estimatedCostUsd={0}
        costCompleteness="none"
      />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('renders Est. label for complete estimates', () => {
    render(
      <EstimatedCostDisplay
        enabled
        estimatedCostUsd={1.23}
        costCompleteness="complete"
      />,
    );
    expect(screen.getByText('Est. $1.23')).toBeInTheDocument();
    expect(screen.queryByLabelText('Incomplete cost estimate')).not.toBeInTheDocument();
  });

  it('renders warning affordance for partial estimates', () => {
    render(
      <EstimatedCostDisplay
        enabled
        estimatedCostUsd={0.0042}
        costCompleteness="partial"
      />,
    );
    expect(screen.getByText('Est. $0.0042')).toBeInTheDocument();
    expect(screen.getByLabelText('Incomplete cost estimate')).toBeInTheDocument();
  });
});
