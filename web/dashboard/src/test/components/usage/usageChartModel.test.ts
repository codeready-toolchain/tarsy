import { describe, expect, it } from 'vitest';
import type { UsageSeriesPoint, UsageSeriesResponse } from '../../../types/api';
import {
  averageGapData,
  cumulativeCostBands,
  cumulativeTotalAt,
  OTHER_SERIES_ID,
} from '../../../components/usage/usageChartModel';

function series(overrides: Partial<UsageSeriesResponse> = {}): UsageSeriesResponse {
  return {
    cost_estimation_enabled: true,
    timezone: 'UTC',
    models: ['model-a', 'model-b'],
    points: [
      {
        start: '2024-06-01T00:00:00Z',
        end: '2024-06-02T00:00:00Z',
        session_count: 1,
        estimated_cost_usd: 1,
        average_cost_usd: 1,
        by_model: { 'model-a': 1 },
      },
      {
        start: '2024-06-02T00:00:00Z',
        end: '2024-06-03T00:00:00Z',
        session_count: 1,
        estimated_cost_usd: 0.75,
        average_cost_usd: 0.75,
        by_model: { 'model-a': 0.25, 'model-b': 0.5 },
      },
    ],
    ...overrides,
  };
}

describe('cumulativeCostBands', () => {
  it('accumulates named models in array order and adds nothing for a missing day', () => {
    const bands = cumulativeCostBands(series());
    expect(bands.map((band) => band.label)).toEqual(['model-a', 'model-b']);
    expect(bands[0].data).toEqual([1, 1.25]);
    expect(bands[1].data).toEqual([0, 0.5]);
    expect(cumulativeTotalAt(bands, 0)).toBeCloseTo(1);
    expect(cumulativeTotalAt(bands, 1)).toBeCloseTo(1.75);
  });

  it('puts Other last, only on days that list it', () => {
    const bands = cumulativeCostBands(
      series({
        models: ['model-a'],
        points: [
          {
            start: '2024-06-01T00:00:00Z',
            end: '2024-06-02T00:00:00Z',
            session_count: 1,
            estimated_cost_usd: 1,
            by_model: { 'model-a': 1 },
          },
          {
            start: '2024-06-02T00:00:00Z',
            end: '2024-06-03T00:00:00Z',
            session_count: 1,
            estimated_cost_usd: 1.4,
            by_model: { 'model-a': 0.4 },
            other_models: [
              { model_name: 'z-tie', estimated_cost_usd: 0.6 },
              { model_name: 'm-rest', estimated_cost_usd: 0.4 },
            ],
          },
        ],
      }),
    );
    expect(bands.map((band) => band.id)).toEqual(['model:model-a', OTHER_SERIES_ID]);
    expect(bands[1].label).toBe('Other');
    expect(bands[1].data).toEqual([0, 1]);
    expect(cumulativeTotalAt(bands, 1)).toBeCloseTo(2.4);
  });
});

function day(sessionCount: number, average?: number): UsageSeriesPoint {
  return {
    start: '2024-06-01T00:00:00Z',
    end: '2024-06-02T00:00:00Z',
    session_count: sessionCount,
    estimated_cost_usd: average ?? 0,
    average_cost_usd: sessionCount === 0 ? undefined : average,
  };
}

describe('averageGapData', () => {
  it('stays empty when every day has sessions', () => {
    expect(averageGapData([day(2, 1), day(1, 0.5)])).toBeNull();
  });

  it('stays empty when empty days are only at the ends', () => {
    expect(averageGapData([day(0), day(2, 1), day(0)])).toBeNull();
  });

  it('repeats the session-day values when an empty day sits between them', () => {
    expect(averageGapData([day(2, 1), day(0), day(1, 0.4)])).toEqual([1, null, 0.4]);
  });
});
