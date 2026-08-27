import { render, screen } from '@testing-library/react';
import TokenUsageDisplay from '../../components/shared/TokenUsageDisplay';

describe('TokenUsageDisplay', () => {
  it('hides cache labels when cache fields are absent', () => {
    render(
      <TokenUsageDisplay
        tokenData={{ input_tokens: 100, output_tokens: 30, total_tokens: 130 }}
        variant="compact"
        size="small"
        label="Tokens"
      />,
    );

    expect(screen.getByText('100')).toBeInTheDocument();
    expect(screen.getByText('30')).toBeInTheDocument();
    expect(screen.getByText('130')).toBeInTheDocument();
    expect(screen.queryByText('cache read')).not.toBeInTheDocument();
    expect(screen.queryByText('cache create')).not.toBeInTheDocument();
  });

  it('shows cache read and cache create when fields are present', () => {
    render(
      <TokenUsageDisplay
        tokenData={{
          input_tokens: 100,
          output_tokens: 30,
          total_tokens: 130,
          cache_read_tokens: 40,
          cache_creation_tokens: 10,
        }}
        variant="compact"
        size="small"
        label="Tokens"
      />,
    );

    expect(screen.getByText('cache read')).toBeInTheDocument();
    expect(screen.getByText('cache create')).toBeInTheDocument();
    expect(screen.getByText('40')).toBeInTheDocument();
    expect(screen.getByText('10')).toBeInTheDocument();
  });

  it('renders cache-only usage when in/out/total are absent', () => {
    render(
      <TokenUsageDisplay
        tokenData={{ cache_read_tokens: 40, cache_creation_tokens: 10 }}
        variant="compact"
        size="small"
        label="Tokens"
      />,
    );

    expect(screen.getByText('cache read')).toBeInTheDocument();
    expect(screen.getByText('cache create')).toBeInTheDocument();
    expect(screen.getByText('40')).toBeInTheDocument();
    expect(screen.getByText('10')).toBeInTheDocument();
  });

  it('renders nothing when all token fields are absent', () => {
    const { container } = render(<TokenUsageDisplay tokenData={{}} variant="compact" />);
    expect(container).toBeEmptyDOMElement();
  });
});
