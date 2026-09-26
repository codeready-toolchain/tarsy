import type { UsageSeriesPoint, UsageSeriesResponse } from '../../types/api.ts';

/**
 * Fixed categorical colors, readable on both Usage paper colors (#fff and #1e1e1e).
 * The last color is reserved for the Other band.
 */
export const USAGE_CHART_COLORS = [
  '#5B86A8',
  '#D08B45',
  '#C46E6E',
  '#5A9E96',
  '#5C9460',
  '#C0A04A',
  '#9678A4',
] as const;

export const OTHER_SERIES_ID = 'usage-other';
export const COST_STACK_ID = 'estimated-cost';

export interface CumulativeCostBand {
  id: string;
  label: string;
  color: string;
  /** Running total of that band through each day. */
  data: number[];
}

/** True when every returned day has no sessions. */
export function seriesWindowIsEmpty(series: UsageSeriesResponse): boolean {
  return (series.points ?? []).every((point) => point.session_count === 0);
}

/**
 * Cumulative cost per named model, then Other.
 * MUI X Charts v9 stacks in array order from the baseline (`stackOrder: 'none'`),
 * so the first model is the bottom band and Other is the top.
 * A missing model on a day adds nothing.
 */
export function cumulativeCostBands(series: UsageSeriesResponse): CumulativeCostBand[] {
  const models = series.models ?? [];
  const points = series.points ?? [];
  const running = models.map(() => 0);
  const named = models.map(() => [] as number[]);
  let otherRunning = 0;
  const otherData: number[] = [];
  let hasOther = false;

  for (const point of points) {
    models.forEach((name, index) => {
      running[index] += point.by_model?.[name] ?? 0;
      named[index].push(running[index]);
    });
    let dayOther = 0;
    for (const row of point.other_models ?? []) {
      dayOther += row.estimated_cost_usd;
      hasOther = true;
    }
    otherRunning += dayOther;
    otherData.push(otherRunning);
  }

  const namedColors = USAGE_CHART_COLORS.length - 1;
  const bands: CumulativeCostBand[] = models.map((name, index) => ({
    id: `model:${name}`,
    label: name,
    color: USAGE_CHART_COLORS[index % namedColors],
    data: named[index],
  }));
  if (hasOther) {
    bands.push({
      id: OTHER_SERIES_ID,
      label: 'Other',
      color: USAGE_CHART_COLORS[USAGE_CHART_COLORS.length - 1],
      data: otherData,
    });
  }
  return bands;
}

/** Sum of each band's own running total — the fleet cumulative through that day. */
export function cumulativeTotalAt(bands: CumulativeCostBand[], index: number): number {
  return bands.reduce((sum, band) => sum + (band.data[index] ?? 0), 0);
}

export function averageCostData(points: UsageSeriesPoint[]): (number | null)[] {
  return points.map((point) => (point.session_count === 0 ? null : (point.average_cost_usd ?? null)));
}

/**
 * Same values as {@link averageCostData} when an empty day sits between two
 * session days, so a connectNulls series can bridge that gap. Null otherwise.
 */
export function averageGapData(points: UsageSeriesPoint[]): (number | null)[] | null {
  const values = averageCostData(points);
  let seenValue = false;
  let seenGap = false;
  for (const value of values) {
    if (value == null) {
      if (seenValue) seenGap = true;
    } else if (seenGap) {
      return values;
    } else {
      seenValue = true;
    }
  }
  return null;
}

/** Accessible name for an average-chart mark. One session is the hollow mark. */
export function sessionMarkLabel(sessionCount: number): string {
  return sessionCount === 1 ? 'One session' : `${sessionCount} sessions`;
}

export function formatSeriesDay(iso: string, timeZone: string): string {
  return new Intl.DateTimeFormat(undefined, {
    timeZone,
    month: 'short',
    day: 'numeric',
  }).format(new Date(iso));
}

export function formatSeriesInterval(start: string, end: string, timeZone: string): string {
  const fmt = new Intl.DateTimeFormat(undefined, {
    timeZone,
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  });
  return `${fmt.format(new Date(start))} – ${fmt.format(new Date(end))}`;
}
