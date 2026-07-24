/**
 * Usage page — fleet dig-in over a date window.
 *
 * Fetches GET /api/v1/usage/summary for server aggregates (totals, breakdowns, top-20).
 * Date presets are Usage-oriented (7d / 30d / MTD / last calendar month); default 30d.
 */

import { useCallback, useEffect, useState, type ReactNode } from 'react';
import { Link as RouterLink } from 'react-router-dom';
import {
  Alert,
  Autocomplete,
  Box,
  Button,
  Chip,
  CircularProgress,
  Container,
  FormControl,
  InputLabel,
  Link,
  MenuItem,
  Paper,
  Select,
  Stack,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  TextField,
  Tooltip,
  Typography,
} from '@mui/material';
import AccessTime from '@mui/icons-material/AccessTime';
import WarningAmberRounded from '@mui/icons-material/WarningAmberRounded';
import FilterList from '@mui/icons-material/FilterList';

import { SharedHeader } from '../components/layout/SharedHeader.tsx';
import { VersionFooter } from '../components/layout/VersionFooter.tsx';
import { FloatingSubmitAlertFab } from '../components/common/FloatingSubmitAlertFab.tsx';
import {
  TimeRangeModal,
  USAGE_TIME_PRESETS,
} from '../components/dashboard/TimeRangeModal.tsx';
import EstimatedCostDisplay from '../components/shared/EstimatedCostDisplay.tsx';
import { getFilterOptions, getUsageSummary, handleAPIError } from '../services/api.ts';
import { formatEstimatedCostUsd, formatTimestamp, formatTokens } from '../utils/format.ts';
import { sessionDetailPath } from '../constants/routes.ts';
import type { UsageRankBy, UsageSummaryResponse } from '../types/api.ts';

function defaultThirtyDayRange(): { start: Date; end: Date; preset: string } {
  const range = USAGE_TIME_PRESETS.find((p) => p.value === '30d')!.getDateRange();
  return { start: range.start, end: range.end, preset: '30d' };
}

/** "~$X" for the Usage page's large, colored cost displays — tilde reads clearly at this size/weight. */
function approxCostUsd(usd: number | null | undefined): string {
  const formatted = formatEstimatedCostUsd(usd);
  return formatted === '—' ? formatted : `~${formatted}`;
}

/** Explains exactly what "Incomplete" means for a model's priced status. */
function incompletePricingTooltip(unpricedCount: number | undefined): string {
  const suffix = 'so its total cost likely undercounts.';
  return unpricedCount != null && unpricedCount > 0
    ? `${unpricedCount} token-bearing interaction${unpricedCount === 1 ? '' : 's'} for this model had no resolved rate (missing from the price catalog/overrides), ${suffix}`
    : `Some token-bearing interactions for this model had no resolved rate (missing from the price catalog/overrides), ${suffix}`;
}

export function UsagePage() {
  const initial = defaultThirtyDayRange();
  const [startDate, setStartDate] = useState<Date>(initial.start);
  const [endDate, setEndDate] = useState<Date>(initial.end);
  const [datePreset, setDatePreset] = useState<string | null>(initial.preset);
  const [timeRangeOpen, setTimeRangeOpen] = useState(false);

  const [alertType, setAlertType] = useState<string | null>(null);
  const [chainId, setChainId] = useState<string | null>(null);
  /** Undefined = let the server pick the default (cost when enabled, else tokens). */
  const [rankBy, setRankBy] = useState<UsageRankBy | undefined>(undefined);

  const [alertTypeOptions, setAlertTypeOptions] = useState<string[]>([]);
  const [chainIdOptions, setChainIdOptions] = useState<string[]>([]);

  const [summary, setSummary] = useState<UsageSummaryResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const opts = await getFilterOptions();
        if (!cancelled) {
          setAlertTypeOptions(opts.alert_types ?? []);
          setChainIdOptions(opts.chain_ids ?? []);
        }
      } catch {
        // Filters remain optional; ignore option load failures.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const fetchSummary = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await getUsageSummary({
        start_date: startDate.toISOString(),
        end_date: endDate.toISOString(),
        alert_type: alertType || undefined,
        chain_id: chainId || undefined,
        rank_by: rankBy,
      });
      setSummary(data);
    } catch (err) {
      setError(handleAPIError(err));
      setSummary(null);
    } finally {
      setLoading(false);
    }
  }, [startDate, endDate, alertType, chainId, rankBy]);

  useEffect(() => {
    void fetchSummary();
  }, [fetchSummary]);

  const handleTimeRangeApply = (start: Date | null, end: Date | null, preset?: string) => {
    if (start && end) {
      setStartDate(start);
      setEndDate(end);
      setDatePreset(preset ?? null);
    } else {
      const fallback = defaultThirtyDayRange();
      setStartDate(fallback.start);
      setEndDate(fallback.end);
      setDatePreset(fallback.preset);
    }
    setTimeRangeOpen(false);
  };

  const costEnabled = summary?.cost_estimation_enabled === true;
  const timeRangeLabel = datePreset
    ? USAGE_TIME_PRESETS.find((p) => p.value === datePreset)?.label ?? `Range: ${datePreset}`
    : 'Custom range';
  const hasActiveFilters = !!alertType || !!chainId;
  const handleClearFilters = () => {
    setAlertType(null);
    setChainId(null);
  };

  return (
    <Box sx={{ minHeight: '100vh', backgroundColor: 'background.default', px: 2, py: 2 }}>
      <SharedHeader title="Usage" showBackButton />

      <Container maxWidth={false} sx={{ py: 4, px: { xs: 1, sm: 2 } }}>
        <Stack spacing={3}>
          {/* Filters */}
          <Paper variant="outlined" sx={{ p: 2 }}>
            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 2 }}>
              <Typography variant="h6" sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                <FilterList fontSize="small" />
                Filters
              </Typography>
              {hasActiveFilters && (
                <Button size="small" onClick={handleClearFilters}>
                  Clear filters
                </Button>
              )}
            </Box>
            <Stack
              direction={{ xs: 'column', md: 'row' }}
              spacing={2}
              alignItems={{ md: 'flex-end' }}
              flexWrap="wrap"
              useFlexGap
            >
              <Button
                variant="outlined"
                startIcon={<AccessTime />}
                onClick={() => setTimeRangeOpen(true)}
                aria-label="Select time range"
                sx={{ height: 40 }}
              >
                {timeRangeLabel}
              </Button>

              <Autocomplete
                size="small"
                sx={{ minWidth: 200 }}
                options={alertTypeOptions}
                value={alertType}
                onChange={(_, value) => setAlertType(value)}
                renderInput={(params) => <TextField {...params} label="Alert type" />}
              />

              <Autocomplete
                size="small"
                sx={{ minWidth: 200 }}
                options={chainIdOptions}
                value={chainId}
                onChange={(_, value) => setChainId(value)}
                renderInput={(params) => <TextField {...params} label="Chain" />}
              />
            </Stack>
          </Paper>

          {error && (
            <Alert severity="error" action={
              <Button color="inherit" size="small" onClick={() => void fetchSummary()}>
                Retry
              </Button>
            }>
              {error}
            </Alert>
          )}

          {loading && !summary && (
            <Box sx={{ display: 'flex', justifyContent: 'center', py: 6 }}>
              <CircularProgress />
            </Box>
          )}

          {summary && (
            <>
              {/* Totals */}
              <Paper variant="outlined" sx={{ p: 2 }}>
                <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 2 }}>
                  <Typography variant="h6">Totals</Typography>
                  {loading && <CircularProgress size={16} />}
                </Box>
                <Box
                  sx={{
                    display: 'grid',
                    gridTemplateColumns: 'repeat(auto-fit, minmax(160px, 1fr))',
                    gap: 2,
                  }}
                >
                  <StatCard label="Total tokens" value={formatTokens(summary.totals.total_tokens)} />
                  <StatCard label="Input tokens" value={formatTokens(summary.totals.input_tokens)} />
                  <StatCard label="Output tokens" value={formatTokens(summary.totals.output_tokens)} />
                  {costEnabled && (
                    <StatCard
                      label="Est. cost"
                      value={approxCostUsd(summary.totals.estimated_cost_usd)}
                      warning={summary.totals.cost_completeness === 'partial'}
                      caption={
                        summary.totals.cost_completeness === 'partial' &&
                        summary.totals.unpriced_interaction_count != null &&
                        summary.totals.unpriced_interaction_count > 0
                          ? `${summary.totals.unpriced_interaction_count} unpriced`
                          : undefined
                      }
                    />
                  )}
                </Box>
              </Paper>

              {/* By model */}
              <BreakdownTable
                title="By model"
                columns={
                  costEnabled
                    ? [
                        { label: 'Model' },
                        { label: 'Tokens', align: 'right' },
                        { label: 'In', align: 'right' },
                        { label: 'Out', align: 'right' },
                        { label: 'Est. cost', align: 'right' },
                        { label: 'Priced', align: 'center' },
                      ]
                    : [
                        { label: 'Model' },
                        { label: 'Tokens', align: 'right' },
                        { label: 'In', align: 'right' },
                        { label: 'Out', align: 'right' },
                      ]
                }
                empty={summary.by_model.length === 0}
              >
                {summary.by_model.map((row) => (
                  <TableRow key={row.model_name} hover>
                    <TableCell>{row.model_name}</TableCell>
                    <TableCell align="right">{formatTokens(row.total_tokens)}</TableCell>
                    <TableCell align="right">{formatTokens(row.input_tokens)}</TableCell>
                    <TableCell align="right">{formatTokens(row.output_tokens)}</TableCell>
                    {costEnabled && (
                      <>
                        <TableCell align="right">{approxCostUsd(row.estimated_cost_usd)}</TableCell>
                        <TableCell align="center">
                          {row.priced === false ? (
                            <Tooltip title={incompletePricingTooltip(row.unpriced_interaction_count)} arrow>
                              <Chip
                                size="small"
                                icon={<WarningAmberRounded />}
                                label="Incomplete"
                                color="warning"
                                variant="outlined"
                              />
                            </Tooltip>
                          ) : (
                            <Chip size="small" label="Priced" color="success" variant="outlined" />
                          )}
                        </TableCell>
                      </>
                    )}
                  </TableRow>
                ))}
              </BreakdownTable>

              {/* By alert type + By chain — side by side, both are narrow */}
              <Box
                sx={{
                  display: 'grid',
                  gridTemplateColumns: { xs: '1fr', md: '1fr 1fr' },
                  gap: 3,
                }}
              >
                <BreakdownTable
                  title="By alert type"
                  columns={
                    costEnabled
                      ? [{ label: 'Alert type' }, { label: 'Tokens', align: 'right' }, { label: 'Est. cost', align: 'right' }]
                      : [{ label: 'Alert type' }, { label: 'Tokens', align: 'right' }]
                  }
                  empty={summary.by_alert_type.length === 0}
                >
                  {summary.by_alert_type.map((row) => (
                    <TableRow key={row.alert_type || '(none)'} hover>
                      <TableCell>{row.alert_type || '—'}</TableCell>
                      <TableCell align="right">{formatTokens(row.total_tokens)}</TableCell>
                      {costEnabled && (
                        <TableCell align="right">{approxCostUsd(row.estimated_cost_usd)}</TableCell>
                      )}
                    </TableRow>
                  ))}
                </BreakdownTable>

                <BreakdownTable
                  title="By chain"
                  columns={
                    costEnabled
                      ? [{ label: 'Chain' }, { label: 'Tokens', align: 'right' }, { label: 'Est. cost', align: 'right' }]
                      : [{ label: 'Chain' }, { label: 'Tokens', align: 'right' }]
                  }
                  empty={summary.by_chain.length === 0}
                >
                  {summary.by_chain.map((row) => (
                    <TableRow key={row.chain_id} hover>
                      <TableCell>{row.chain_id}</TableCell>
                      <TableCell align="right">{formatTokens(row.total_tokens)}</TableCell>
                      {costEnabled && (
                        <TableCell align="right">{approxCostUsd(row.estimated_cost_usd)}</TableCell>
                      )}
                    </TableRow>
                  ))}
                </BreakdownTable>
              </Box>

              {/* Top sessions */}
              <BreakdownTable
                title={`Top sessions (${summary.top_sessions.length})`}
                action={
                  costEnabled && (
                    <FormControl size="small" sx={{ minWidth: 160 }}>
                      <InputLabel id="usage-rank-by-label">Rank top sessions</InputLabel>
                      <Select
                        labelId="usage-rank-by-label"
                        label="Rank top sessions"
                        value={rankBy ?? summary?.rank_by ?? 'cost'}
                        onChange={(e) => setRankBy(e.target.value as UsageRankBy)}
                      >
                        <MenuItem value="cost">By cost</MenuItem>
                        <MenuItem value="tokens">By tokens</MenuItem>
                      </Select>
                    </FormControl>
                  )
                }
                columns={
                  costEnabled
                    ? [
                        { label: 'Session' },
                        { label: 'Alert type' },
                        { label: 'Chain' },
                        { label: 'Tokens', align: 'right' },
                        { label: 'Est. cost', align: 'right' },
                        { label: 'Created', align: 'right' },
                      ]
                    : [
                        { label: 'Session' },
                        { label: 'Alert type' },
                        { label: 'Chain' },
                        { label: 'Tokens', align: 'right' },
                        { label: 'Created', align: 'right' },
                      ]
                }
                empty={summary.top_sessions.length === 0}
              >
                {summary.top_sessions.map((row) => (
                  <TableRow key={row.session_id} hover>
                    <TableCell>
                      <Link
                        component={RouterLink}
                        to={sessionDetailPath(row.session_id)}
                        underline="hover"
                        sx={{ fontFamily: 'monospace', fontSize: '0.85em' }}
                      >
                        {row.session_id.slice(0, 8)}…
                      </Link>
                    </TableCell>
                    <TableCell>{row.alert_type || '—'}</TableCell>
                    <TableCell>{row.chain_id}</TableCell>
                    <TableCell align="right">{formatTokens(row.total_tokens)}</TableCell>
                    {costEnabled && (
                      <TableCell align="right">
                        <EstimatedCostDisplay
                          enabled
                          estimatedCostUsd={row.estimated_cost_usd}
                          costCompleteness={row.cost_completeness}
                          size="small"
                        />
                      </TableCell>
                    )}
                    <TableCell align="right">{formatTimestamp(row.created_at, 'short')}</TableCell>
                  </TableRow>
                ))}
              </BreakdownTable>
            </>
          )}
        </Stack>

        <VersionFooter />
      </Container>

      <TimeRangeModal
        open={timeRangeOpen}
        onClose={() => setTimeRangeOpen(false)}
        startDate={startDate}
        endDate={endDate}
        onApply={handleTimeRangeApply}
        presets={USAGE_TIME_PRESETS}
      />

      <FloatingSubmitAlertFab />
    </Box>
  );
}

interface BreakdownColumn {
  label: string;
  align?: 'left' | 'right' | 'center';
}

function BreakdownTable({
  title,
  action,
  columns,
  empty,
  children,
}: {
  title: string;
  /** Optional control affecting only this table's rows (e.g. a sort order), rendered next to the title. */
  action?: ReactNode;
  columns: BreakdownColumn[];
  empty: boolean;
  children: ReactNode;
}) {
  return (
    <Paper variant="outlined" sx={{ p: 2, height: '100%' }}>
      <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 2, flexWrap: 'wrap', gap: 1 }}>
        <Typography variant="h6">{title}</Typography>
        {action}
      </Box>
      {empty ? (
        <Typography variant="body2" color="text.secondary">
          No data in this window.
        </Typography>
      ) : (
        <TableContainer>
          <Table
            size="small"
            sx={{
              '& thead th': {
                fontWeight: 700,
                fontSize: '0.7rem',
                textTransform: 'uppercase',
                letterSpacing: 0.4,
                color: 'text.secondary',
                borderBottomWidth: 2,
              },
              '& tbody td': { fontVariantNumeric: 'tabular-nums' },
              '& tbody tr:last-child td': { borderBottom: 0 },
            }}
          >
            <TableHead>
              <TableRow>
                {columns.map((col) => (
                  <TableCell key={col.label} align={col.align ?? 'left'}>
                    {col.label}
                  </TableCell>
                ))}
              </TableRow>
            </TableHead>
            <TableBody>{children}</TableBody>
          </Table>
        </TableContainer>
      )}
    </Paper>
  );
}

function StatCard({
  label,
  value,
  warning,
  caption,
}: {
  label: string;
  value: string;
  warning?: boolean;
  caption?: string;
}) {
  return (
    <Box sx={{ p: 1.5, borderRadius: 1, bgcolor: 'action.hover' }}>
      <Typography
        variant="caption"
        color="text.secondary"
        sx={{ textTransform: 'uppercase', letterSpacing: 0.5, fontWeight: 600, display: 'block' }}
      >
        {label}
      </Typography>
      <Typography
        variant="h5"
        fontWeight={700}
        color={warning ? 'warning.main' : 'text.primary'}
        sx={{ mt: 0.25, lineHeight: 1.2 }}
      >
        {value}
      </Typography>
      {caption && (
        <Typography
          variant="caption"
          color="warning.main"
          sx={{ display: 'flex', alignItems: 'center', gap: 0.25, mt: 0.25 }}
        >
          <WarningAmberRounded sx={{ fontSize: '0.9rem' }} />
          {caption}
        </Typography>
      )}
    </Box>
  );
}
