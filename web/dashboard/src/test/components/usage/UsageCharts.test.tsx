import type { ReactElement } from 'react';
import { render, screen } from '@testing-library/react';
import { ThemeProvider } from '@mui/material/styles';
import { theme } from '../../../theme';
import { AverageCostColumn, CostTooltipBody } from '../../../components/usage/UsageCharts';
import type { UsageSeriesPoint } from '../../../types/api';

function renderWithTheme(ui: ReactElement) {
  return render(<ThemeProvider theme={theme}>{ui}</ThemeProvider>);
}

const point: UsageSeriesPoint = {
  start: '2024-06-02T07:00:00.000Z',
  end: '2024-06-03T00:00:00.000Z',
  session_count: 2,
  estimated_cost_usd: 1.4,
  average_cost_usd: 0.7,
  by_model: { 'model-a': 0.4 },
  other_models: [
    { model_name: 'z-tie', estimated_cost_usd: 0.6 },
    { model_name: 'm-rest', estimated_cost_usd: 0.4 },
  ],
};

describe('CostTooltipBody', () => {
  it('lists named day costs and the Other models', () => {
    renderWithTheme(
      <CostTooltipBody point={point} models={['model-a']} cumulativeUsd={2.4} timeZone="UTC" />,
    );

    expect(screen.getByText('Est.')).toBeInTheDocument();
    expect(screen.getByText('model-a')).toBeInTheDocument();
    expect(screen.getAllByText('$0.400').length).toBeGreaterThan(0);
    expect(screen.getByText('Other')).toBeInTheDocument();
    expect(screen.getByText('z-tie')).toBeInTheDocument();
    expect(screen.getByText('$0.600')).toBeInTheDocument();
    expect(screen.getByText('m-rest')).toBeInTheDocument();
    expect(screen.getByText('Day total')).toBeInTheDocument();
    expect(screen.getByText('$1.40')).toBeInTheDocument();
    expect(screen.getByText('Cumulative')).toBeInTheDocument();
    expect(screen.getByText('$2.40')).toBeInTheDocument();
  });
});

describe('AverageCostColumn', () => {
  it('outlines a one-session day and fills a day with more sessions', () => {
    const { rerender } = render(
      <svg>
        <AverageCostColumn x={10} y={12} width={8} height={40} color="#3B6D9A" sessionCount={1} />
      </svg>,
    );
    const outlined = screen.getByRole('img', { name: 'One session' });
    expect(outlined).toHaveAttribute('fill', 'transparent');
    expect(outlined).toHaveAttribute('stroke', '#3B6D9A');

    rerender(
      <svg>
        <AverageCostColumn x={10} y={12} width={8} height={40} color="#3B6D9A" sessionCount={4} />
      </svg>,
    );
    const filled = screen.getByRole('img', { name: '4 sessions' });
    expect(filled).toHaveAttribute('fill', '#3B6D9A');
    expect(filled).toHaveAttribute('stroke', 'none');
  });

  it('keeps a zero-cost day visible on the baseline', () => {
    render(
      <svg>
        <AverageCostColumn x={10} y={20} width={8} height={0} color="#3B6D9A" sessionCount={3} />
      </svg>,
    );
    const column = screen.getByRole('img', { name: '3 sessions' });
    expect(Number(column.getAttribute('height'))).toBeGreaterThan(0);
    expect(Number(column.getAttribute('y'))).toBeLessThan(20);
  });
});
