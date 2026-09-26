import type { ReactElement } from 'react';
import { render, screen } from '@testing-library/react';
import { ThemeProvider } from '@mui/material/styles';
import { theme } from '../../../theme';
import { AverageChart, AverageCostColumn, CostTooltipBody } from '../../../components/usage/UsageCharts';
import type { UsageSeriesPoint, UsageSeriesResponse } from '../../../types/api';

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

function usageSeries(points: UsageSeriesPoint[]): UsageSeriesResponse {
  return {
    cost_estimation_enabled: true,
    timezone: 'UTC',
    points,
  };
}

function day(start: string, end: string, sessionCount: number, average: number): UsageSeriesPoint {
  return {
    start,
    end,
    session_count: sessionCount,
    estimated_cost_usd: average * sessionCount,
    average_cost_usd: sessionCount === 0 ? undefined : average,
  };
}

/** Tick labels sit in a translated group, so the axis position is the group's y plus the text y. */
function tickBaselineY(svg: SVGElement, label: string): number {
  const text = [...svg.querySelectorAll('text')].find((node) => node.textContent === label);
  if (!text) throw new Error(`missing tick ${label}`);
  const transform = text.closest('g')?.getAttribute('transform') ?? '';
  const match = /translate\(\s*[^,]+,\s*([-0-9.]+)\s*\)/.exec(transform);
  return (match ? Number(match[1]) : 0) + Number(text.getAttribute('y'));
}

function columnBottom(name: string): { bottom: number; svg: SVGElement } {
  const column = screen.getByRole('img', { name, hidden: true });
  const svg = column.closest('svg');
  if (!svg) throw new Error('column is not in a chart');
  return {
    bottom: Number(column.getAttribute('y')) + Number(column.getAttribute('height')),
    svg,
  };
}

describe('AverageChart axis', () => {
  it('keeps an all-zero window on the baseline under a positive maximum', () => {
    renderWithTheme(
      <AverageChart
        series={usageSeries([
          day('2024-06-01T00:00:00.000Z', '2024-06-02T00:00:00.000Z', 2, 0),
          day('2024-06-02T00:00:00.000Z', '2024-06-03T00:00:00.000Z', 3, 0),
        ])}
        width={640}
      />,
    );

    const { bottom, svg } = columnBottom('2 sessions');
    const zeroY = tickBaselineY(svg, '$0.00');
    expect(tickBaselineY(svg, '$1.00')).toBeLessThan(zeroY);
    expect(bottom).toBeCloseTo(zeroY, 0);
  });

  it('keeps a positive average on a scale above $1', () => {
    renderWithTheme(
      <AverageChart
        series={usageSeries([day('2024-06-01T00:00:00.000Z', '2024-06-02T00:00:00.000Z', 2, 12)])}
        width={640}
      />,
    );

    const { bottom, svg } = columnBottom('2 sessions');
    const zeroY = tickBaselineY(svg, '$0.00');
    expect(tickBaselineY(svg, '$12.00')).toBeLessThan(zeroY);
    expect(bottom).toBeCloseTo(zeroY, 0);
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
