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
  cumulativeCostBands,
  cumulativeTotalAt,
  formatSeriesDay,
  formatSeriesInterval,
  seriesWindowIsEmpty,
  sessionMarkLabel,
} from './usageChartModel.ts';

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
      height={320}
      skipAnimation
      hideLegend={bands.length === 0}
      series={chartSeries}
      xAxis={dayAxis(points, timeZone)}
      yAxis={costAxis()}
      slots={{ tooltip: TooltipSlot }}
    />
  );
}

function AverageChart({ series }: { series: UsageSeriesResponse }) {
  const points = series.points ?? [];
  const timeZone = series.timezone || 'UTC';
  const color = USAGE_CHART_COLORS[0];

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
      height={220}
      skipAnimation
      hideLegend
      series={[
        {
          id: 'average-cost',
          data: averageCostData(points),
          color,
          curve: 'linear',
          shape: 'circle',
          showMark: ({ index }) => (points[index]?.session_count ?? 0) > 0,
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
  return (
    <>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 1 }}>
        <Typography variant="h6">Est. cost by model</Typography>
      </Box>
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
      <CostChart series={series} />
      <Typography variant="h6" sx={{ mt: 2, mb: 1 }}>
        Avg. cost / session
      </Typography>
      <AverageChart series={series} />
    </>
  );
}
