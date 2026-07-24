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
  Typography,
} from '@mui/material';
import AccessTime from '@mui/icons-material/AccessTime';
import WarningAmberRounded from '@mui/icons-material/WarningAmberRounded';

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

  return (
    <Box sx={{ minHeight: '100vh', backgroundColor: 'background.default', px: 2, py: 2 }}>
      <SharedHeader title="Usage" showBackButton />

      <Container maxWidth={false} sx={{ py: 4, px: { xs: 1, sm: 2 } }}>
        <Stack spacing={3}>
          {/* Filters */}
          <Paper variant="outlined" sx={{ p: 2 }}>
            <Stack
              direction={{ xs: 'column', md: 'row' }}
              spacing={2}
              alignItems={{ md: 'center' }}
              flexWrap="wrap"
              useFlexGap
            >
              <Button
                variant="outlined"
                startIcon={<AccessTime />}
                onClick={() => setTimeRangeOpen(true)}
                aria-label="Select time range"
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

              {costEnabled && (
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
              )}
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
                <Typography variant="h6" gutterBottom>
                  Totals
                </Typography>
                <Stack direction="row" spacing={3} flexWrap="wrap" useFlexGap alignItems="center">
                  <Typography variant="body1">
                    <strong>{formatTokens(summary.totals.total_tokens)}</strong>{' '}
                    <Typography component="span" color="text.secondary" variant="body2">
                      tokens
                    </Typography>
                  </Typography>
                  <Typography variant="body2" color="text.secondary">
                    {formatTokens(summary.totals.input_tokens)} in ·{' '}
                    {formatTokens(summary.totals.output_tokens)} out
                  </Typography>
                  {costEnabled && (
                    <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
                      <EstimatedCostDisplay
                        enabled
                        estimatedCostUsd={summary.totals.estimated_cost_usd}
                        costCompleteness={summary.totals.cost_completeness}
                        variant="labeled"
                      />
                      {summary.totals.cost_completeness === 'partial' &&
                        summary.totals.unpriced_interaction_count != null &&
                        summary.totals.unpriced_interaction_count > 0 && (
                          <Typography variant="caption" color="warning.main">
                            ({summary.totals.unpriced_interaction_count} unpriced)
                          </Typography>
                        )}
                    </Box>
                  )}
                  {loading && <CircularProgress size={18} />}
                </Stack>
              </Paper>

              {/* By model */}
              <BreakdownTable
                title="By model"
                columns={
                  costEnabled
                    ? ['Model', 'Tokens', 'In', 'Out', 'Est. cost', 'Priced']
                    : ['Model', 'Tokens', 'In', 'Out']
                }
                empty={summary.by_model.length === 0}
              >
                {summary.by_model.map((row) => (
                  <TableRow key={row.model_name}>
                    <TableCell>{row.model_name}</TableCell>
                    <TableCell>{formatTokens(row.total_tokens)}</TableCell>
                    <TableCell>{formatTokens(row.input_tokens)}</TableCell>
                    <TableCell>{formatTokens(row.output_tokens)}</TableCell>
                    {costEnabled && (
                      <>
                        <TableCell>
                          {row.estimated_cost_usd != null
                            ? formatEstimatedCostUsd(row.estimated_cost_usd)
                            : '—'}
                        </TableCell>
                        <TableCell>
                          {row.priced === false ? (
                            <Chip
                              size="small"
                              icon={<WarningAmberRounded />}
                              label="Incomplete"
                              color="warning"
                              variant="outlined"
                            />
                          ) : (
                            <Chip size="small" label="Priced" color="success" variant="outlined" />
                          )}
                        </TableCell>
                      </>
                    )}
                  </TableRow>
                ))}
              </BreakdownTable>

              {/* By alert type */}
              <BreakdownTable
                title="By alert type"
                columns={costEnabled ? ['Alert type', 'Tokens', 'Est. cost'] : ['Alert type', 'Tokens']}
                empty={summary.by_alert_type.length === 0}
              >
                {summary.by_alert_type.map((row) => (
                  <TableRow key={row.alert_type || '(none)'}>
                    <TableCell>{row.alert_type || '—'}</TableCell>
                    <TableCell>{formatTokens(row.total_tokens)}</TableCell>
                    {costEnabled && (
                      <TableCell>
                        {row.estimated_cost_usd != null
                          ? formatEstimatedCostUsd(row.estimated_cost_usd)
                          : '—'}
                      </TableCell>
                    )}
                  </TableRow>
                ))}
              </BreakdownTable>

              {/* By chain */}
              <BreakdownTable
                title="By chain"
                columns={costEnabled ? ['Chain', 'Tokens', 'Est. cost'] : ['Chain', 'Tokens']}
                empty={summary.by_chain.length === 0}
              >
                {summary.by_chain.map((row) => (
                  <TableRow key={row.chain_id}>
                    <TableCell>{row.chain_id}</TableCell>
                    <TableCell>{formatTokens(row.total_tokens)}</TableCell>
                    {costEnabled && (
                      <TableCell>
                        {row.estimated_cost_usd != null
                          ? formatEstimatedCostUsd(row.estimated_cost_usd)
                          : '—'}
                      </TableCell>
                    )}
                  </TableRow>
                ))}
              </BreakdownTable>

              {/* Top sessions */}
              <BreakdownTable
                title={`Top sessions (${summary.top_sessions.length})`}
                columns={
                  costEnabled
                    ? ['Session', 'Alert type', 'Chain', 'Tokens', 'Est. cost', 'Created']
                    : ['Session', 'Alert type', 'Chain', 'Tokens', 'Created']
                }
                empty={summary.top_sessions.length === 0}
              >
                {summary.top_sessions.map((row) => (
                  <TableRow key={row.session_id}>
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
                    <TableCell>{formatTokens(row.total_tokens)}</TableCell>
                    {costEnabled && (
                      <TableCell>
                        <EstimatedCostDisplay
                          enabled
                          estimatedCostUsd={row.estimated_cost_usd}
                          costCompleteness={row.cost_completeness}
                          size="small"
                        />
                      </TableCell>
                    )}
                    <TableCell>{formatTimestamp(row.created_at, 'short')}</TableCell>
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

function BreakdownTable({
  title,
  columns,
  empty,
  children,
}: {
  title: string;
  columns: string[];
  empty: boolean;
  children: ReactNode;
}) {
  return (
    <Paper variant="outlined" sx={{ p: 2 }}>
      <Typography variant="h6" gutterBottom>
        {title}
      </Typography>
      {empty ? (
        <Typography variant="body2" color="text.secondary">
          No data in this window.
        </Typography>
      ) : (
        <TableContainer>
          <Table size="small">
            <TableHead>
              <TableRow>
                {columns.map((col) => (
                  <TableCell key={col}>{col}</TableCell>
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
