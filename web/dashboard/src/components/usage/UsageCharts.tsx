import type { ReactNode } from 'react';
import { Alert, Box, Button, CircularProgress, Paper, Tooltip, Typography } from '@mui/material';
import WarningAmberRounded from '@mui/icons-material/WarningAmberRounded';
import { LineChart } from '@mui/x-charts/LineChart';
import { ChartsTooltipContainer, useAxesTooltip } from '@mui/x-charts/ChartsTooltip';
import type { MarkElementProps } from '@mui/x-charts/LineChart';
import { formatEstimatedCostUsd } from '../../utils/format.ts';
import type { UsageSeriesPoint, UsageSeriesResponse } from '../../types/api.ts';
import {
  COST_STACK_ID,
  USAGE_CHART_COLORS,
  averageCostData,
  averageGapData,
  cumulativeCostBands,
  type CumulativeCostBand,
  cumulativeTotalAt,
  formatSeriesDay,
  formatSeriesInterval,
  seriesWindowIsEmpty,
  sessionMarkLabel,
} from './usageChartModel.ts';

/** Both charts use this height so their axes share a top and a baseline. */
const CHART_HEIGHT = 320;

const tooltipSurfaceSx = {
  bgcolor: 'background.paper',
  color: 'text.primary',
  border: 1,
  borderColor: 'divider',
  borderRadius: 1,
  boxShadow: 3,
  p: 1.25,
  minWidth: 200,
  pointerEvents: 'none',
} as const;

function TooltipRow({ label, value, inset }: { label: string; value: string; inset?: boolean }) {
  return (
    <Box
      sx={{
        display: 'flex',
        justifyContent: 'space-between',
        gap: 2,
        pl: inset ? 1.5 : 0,
      }}
    >
      <Typography variant="body2" color={inset ? 'text.secondary' : 'text.primary'}>
        {label}
      </Typography>
      <Typography variant="body2">{value}</Typography>
    </Box>
  );
}

/** Day-delta cost tooltip. Chart series values are cumulative and are not shown here. */
export function CostTooltipBody({
  point,
  models,
  cumulativeUsd,
  timeZone,
}: {
  point: UsageSeriesPoint;
  models: string[];
  cumulativeUsd: number;
  timeZone: string;
}) {
  const other = point.other_models ?? [];
  const otherTotal = other.reduce((sum, row) => sum + row.estimated_cost_usd, 0);
  return (
    <Box sx={tooltipSurfaceSx}>
      <Typography variant="subtitle2">Est.</Typography>
      <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mb: 0.75 }}>
        {formatSeriesInterval(point.start, point.end, timeZone)}
      </Typography>
      {models.map((name) => (
        <TooltipRow
          key={name}
          label={name}
          value={formatEstimatedCostUsd(point.by_model?.[name] ?? 0)}
        />
      ))}
      {other.length > 0 && (
        <>
          <TooltipRow label="Other" value={formatEstimatedCostUsd(otherTotal)} />
          {other.map((row) => (
            <TooltipRow
              key={row.model_name}
              label={row.model_name}
              value={formatEstimatedCostUsd(row.estimated_cost_usd)}
              inset
            />
          ))}
        </>
      )}
      <TooltipRow label="Day total" value={formatEstimatedCostUsd(point.estimated_cost_usd)} />
      <TooltipRow label="Cumulative" value={formatEstimatedCostUsd(cumulativeUsd)} />
    </Box>
  );
}

export function AverageTooltipBody({
  point,
  timeZone,
}: {
  point: UsageSeriesPoint;
  timeZone: string;
}) {
  const count = point.session_count;
  const countLabel = count === 1 ? '1 session' : `${count.toLocaleString()} sessions`;
  return (
    <Box sx={tooltipSurfaceSx}>
      <Typography variant="subtitle2">
        Est. {formatEstimatedCostUsd(point.average_cost_usd)}
      </Typography>
      <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mb: 0.5 }}>
        {formatSeriesInterval(point.start, point.end, timeZone)}
      </Typography>
      <Typography variant="body2">{countLabel}</Typography>
      {count === 1 && (
        <Typography variant="body2">This figure is that session&apos;s cost.</Typography>
      )}
    </Box>
  );
}

/** Hollow when the day has one session; filled otherwise. */
export function AverageSessionMark({
  x,
  y,
  color,
  sessionCount,
}: {
  x: number;
  y: number;
  color: string;
  sessionCount: number;
}) {
  const hollow = sessionCount === 1;
  return (
    <circle
      cx={x}
      cy={y}
      r={hollow ? 4 : 3.5}
      fill={hollow ? 'transparent' : color}
      stroke={color}
      strokeWidth={2}
      role="img"
      aria-label={sessionMarkLabel(sessionCount)}
    />
  );
}

function dayAxis(points: UsageSeriesPoint[], timeZone: string) {
  return [
    {
      scaleType: 'point' as const,
      data: points.map((point) => point.start),
      valueFormatter: (value: string) => formatSeriesDay(value, timeZone),
    },
  ];
}

function costAxis() {
  return [
    {
      width: 72,
      valueFormatter: (value: number | null) => formatEstimatedCostUsd(value),
    },
  ];
}

function CostChart({ series }: { series: UsageSeriesResponse }) {
  const points = series.points ?? [];
  const timeZone = series.timezone || 'UTC';
  const bands = cumulativeCostBands(series);
  const models = series.models ?? [];

  function TooltipSlot() {
    const axes = useAxesTooltip();
    const index = axes?.[0]?.dataIndex;
    const point = index == null ? undefined : points[index];
    return (
      <ChartsTooltipContainer trigger="axis">
        {point ? (
          <CostTooltipBody
            point={point}
            models={models}
            cumulativeUsd={index == null ? 0 : cumulativeTotalAt(bands, index)}
            timeZone={timeZone}
          />
        ) : null}
      </ChartsTooltipContainer>
    );
  }

  const chartSeries =
    bands.length > 0
      ? bands.map((band) => ({
          id: band.id,
          label: band.label,
          data: band.data,
          color: band.color,
          labelMarkType: 'square' as const,
          stack: COST_STACK_ID,
          area: true,
          curve: 'linear' as const,
          showMark: false,
        }))
      : [
          {
            id: 'zero',
            data: points.map(() => 0),
            color: '#6B7280',
            showMark: false,
            curve: 'linear' as const,
          },
        ];

  return (
    <LineChart
      height={CHART_HEIGHT}
      skipAnimation
      hideLegend
      series={chartSeries}
      xAxis={dayAxis(points, timeZone)}
      yAxis={costAxis()}
      slots={{ tooltip: TooltipSlot }}
    />
  );
}

/** Drawn outside the plot so it does not change the cost chart's height. */
function CostLegend({ bands }: { bands: CumulativeCostBand[] }) {
  if (bands.length === 0) return null;
  return (
    <Box
      component="ul"
      sx={{
        display: 'flex',
        flexWrap: 'wrap',
        justifyContent: 'center',
        alignItems: 'center',
        gap: 2,
        m: 0,
        p: 0,
        listStyle: 'none',
      }}
    >
      {bands.map((band) => (
        <Box
          component="li"
          key={band.id}
          sx={{ display: 'flex', alignItems: 'center', gap: 0.75 }}
        >
          <Box
            aria-hidden
            sx={{
              width: 16,
              height: 16,
              bgcolor: band.color,
              borderRadius: 0.25,
              flexShrink: 0,
            }}
          />
          <Typography variant="caption">{band.label}</Typography>
        </Box>
      ))}
    </Box>
  );
}

function AverageChart({ series }: { series: UsageSeriesResponse }) {
  const points = series.points ?? [];
  const timeZone = series.timezone || 'UTC';
  const color = USAGE_CHART_COLORS[0];
  const gapData = averageGapData(points);

  function TooltipSlot() {
    const axes = useAxesTooltip();
    const index = axes?.[0]?.dataIndex;
    const point = index == null ? undefined : points[index];
    return (
      <ChartsTooltipContainer trigger="axis">
        {point ? <AverageTooltipBody point={point} timeZone={timeZone} /> : null}
      </ChartsTooltipContainer>
    );
  }

  function MarkSlot(props: MarkElementProps) {
    const count = points[props.dataIndex]?.session_count ?? 0;
    return (
      <AverageSessionMark
        x={Number(props.x)}
        y={Number(props.y)}
        color={typeof props.color === 'string' ? props.color : color}
        sessionCount={count}
      />
    );
  }

  return (
    <LineChart
      height={CHART_HEIGHT}
      skipAnimation
      hideLegend
      sx={{
        '& .MuiLineChart-line[data-series-id="average-gap"]': {
          strokeDasharray: '2 4',
        },
      }}
      series={[
        // Under the solid line. connectNulls bridges empty days; the solid
        // series covers the same path wherever neighboring days both have sessions.
        ...(gapData
          ? [
              {
                id: 'average-gap',
                data: gapData,
                color,
                curve: 'linear' as const,
                connectNulls: true,
                showMark: false,
                disableHighlight: true,
              },
            ]
          : []),
        {
          id: 'average-cost',
          data: averageCostData(points),
          color,
          curve: 'linear' as const,
          shape: 'circle' as const,
          showMark: ({ index }: { index: number }) => (points[index]?.session_count ?? 0) > 0,
        },
      ]}
      xAxis={dayAxis(points, timeZone)}
      yAxis={costAxis()}
      slots={{ tooltip: TooltipSlot, mark: MarkSlot }}
    />
  );
}

export function UsageCharts({
  series,
  loading,
  error,
  onRetry,
  partialCaption,
  partialTooltip,
}: {
  series: UsageSeriesResponse | null;
  loading: boolean;
  error: string | null;
  onRetry: () => void;
  partialCaption?: string;
  partialTooltip?: string;
}) {
  const empty = series != null && seriesWindowIsEmpty(series);
  let body: ReactNode = null;
  if (series && !empty) {
    body = (
      <StackCharts
        series={series}
        partialCaption={partialCaption}
        partialTooltip={partialTooltip}
      />
    );
  } else if (empty) {
    body = (
      <Typography variant="body2" sx={{ color: 'text.secondary' }}>
        No data in this window.
      </Typography>
    );
  } else if (loading) {
    body = (
      <Box sx={{ display: 'flex', justifyContent: 'center', py: 4 }}>
        <CircularProgress size={24} />
      </Box>
    );
  }

  if (!error && !body) return null;

  return (
    <Paper variant="outlined" sx={{ p: 2 }}>
      {error && (
        <Alert
          severity="error"
          sx={{ mb: body ? 2 : 0 }}
          action={
            <Button color="inherit" size="small" onClick={onRetry}>
              Retry
            </Button>
          }
        >
          {error}
        </Alert>
      )}
      {body}
    </Paper>
  );
}

function StackCharts({
  series,
  partialCaption,
  partialTooltip,
}: {
  series: UsageSeriesResponse;
  partialCaption?: string;
  partialTooltip?: string;
}) {
  const bands = cumulativeCostBands(series);
  const hasLegend = bands.length > 0;
  const wideAreas = hasLegend
    ? '"cost-title avg-title" "cost-legend ." "cost-chart avg-chart"'
    : '"cost-title avg-title" "cost-chart avg-chart"';
  const narrowAreas = hasLegend
    ? '"cost-title" "cost-legend" "cost-chart" "avg-title" "avg-chart"'
    : '"cost-title" "cost-chart" "avg-title" "avg-chart"';

  return (
    <Box sx={{ containerType: 'inline-size' }}>
      {partialCaption && (
        <Tooltip title={partialTooltip ?? ''} disableHoverListener={!partialTooltip} arrow>
          <Typography
            variant="caption"
            sx={{
              color: 'warning.main',
              display: 'flex',
              alignItems: 'center',
              gap: 0.25,
              mb: 1,
              width: 'fit-content',
            }}
          >
            <WarningAmberRounded sx={{ fontSize: '0.9rem' }} />
            {partialCaption}
          </Typography>
        </Tooltip>
      )}
      <Box
        sx={{
          display: 'grid',
          columnGap: 2,
          rowGap: 1,
          alignItems: 'start',
          gridTemplateColumns: 'minmax(0, 1fr)',
          gridTemplateAreas: narrowAreas,
          // The query has to live on a child; a container cannot query itself.
          '@container (min-width: 860px)': {
            gridTemplateColumns: 'repeat(2, minmax(0, 1fr))',
            gridTemplateAreas: wideAreas,
          },
        }}
      >
        <Typography variant="h6" sx={{ gridArea: 'cost-title' }}>
          Est. cost by model
        </Typography>
        <Typography variant="h6" sx={{ gridArea: 'avg-title' }}>
          Avg. cost / session
        </Typography>
        {hasLegend && (
          <Box sx={{ gridArea: 'cost-legend', minWidth: 0 }}>
            <CostLegend bands={bands} />
          </Box>
        )}
        <Box sx={{ gridArea: 'cost-chart', minWidth: 0 }}>
          <CostChart series={series} />
        </Box>
        <Box sx={{ gridArea: 'avg-chart', minWidth: 0 }}>
          <AverageChart series={series} />
        </Box>
      </Box>
    </Box>
  );
}
