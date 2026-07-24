/**
 * TimeRangeModal — preset and custom time range selection dialog.
 *
 * Ported from old dashboard's TimeRangeModal.tsx.
 * Provides both preset time ranges (10m, 1h, 12h, 1d, 7d, 30d) and
 * custom date/time selection in a modal dialog, matching old dashboard UX.
 */

import { useState } from 'react';
import {
  Dialog,
  DialogTitle,
  DialogContent,
  DialogActions,
  Button,
  Box,
  Typography,
  Chip,
  Stack,
  IconButton,
  Divider,
  Alert,
} from '@mui/material';
import { Close, AccessTime, CalendarToday } from '@mui/icons-material';
import { DateTimePicker } from '@mui/x-date-pickers/DateTimePicker';
import { LocalizationProvider } from '@mui/x-date-pickers/LocalizationProvider';
import { AdapterDateFns } from '@mui/x-date-pickers/AdapterDateFns';
import {
  format,
  subMinutes,
  subHours,
  subDays,
  isBefore,
  startOfMonth,
  subMonths,
} from 'date-fns';

export interface TimePreset {
  label: string;
  value: string;
  description: string;
  getDateRange: () => { start: Date; end: Date };
}

export interface TimeRangeModalProps {
  open: boolean;
  onClose: () => void;
  startDate?: Date | null;
  endDate?: Date | null;
  /** Preset value currently applied, if the applied range came from a preset (vs. custom dates). */
  activePreset?: string | null;
  onApply: (startDate: Date | null, endDate: Date | null, preset?: string) => void;
  /** Override presets (defaults to Alert History quick ranges). */
  presets?: TimePreset[];
}

/** Default Alert History presets (10m … 30d). */
export const DEFAULT_TIME_PRESETS: TimePreset[] = [
  {
    label: 'Last 10 minutes',
    value: '10m',
    description: 'Last 10 minutes',
    getDateRange: () => ({
      start: subMinutes(new Date(), 10),
      end: new Date(),
    }),
  },
  {
    label: 'Last hour',
    value: '1h',
    description: 'Last hour',
    getDateRange: () => ({
      start: subHours(new Date(), 1),
      end: new Date(),
    }),
  },
  {
    label: 'Last 12 hours',
    value: '12h',
    description: 'Last 12 hours',
    getDateRange: () => ({
      start: subHours(new Date(), 12),
      end: new Date(),
    }),
  },
  {
    label: 'Last day',
    value: '1d',
    description: 'Last 24 hours',
    getDateRange: () => ({
      start: subDays(new Date(), 1),
      end: new Date(),
    }),
  },
  {
    label: 'Last 7 days',
    value: '7d',
    description: 'Last week',
    getDateRange: () => ({
      start: subDays(new Date(), 7),
      end: new Date(),
    }),
  },
  {
    label: 'Last 30 days',
    value: '30d',
    description: 'Last month',
    getDateRange: () => ({
      start: subDays(new Date(), 30),
      end: new Date(),
    }),
  },
];

/** Usage page presets (1d / 7d / 30d / MTD / last calendar month). */
export const USAGE_TIME_PRESETS: TimePreset[] = [
  {
    label: 'Last day',
    value: '1d',
    description: 'Last 24 hours',
    getDateRange: () => ({
      start: subDays(new Date(), 1),
      end: new Date(),
    }),
  },
  {
    label: 'Last 7 days',
    value: '7d',
    description: 'Last 7 days',
    getDateRange: () => ({
      start: subDays(new Date(), 7),
      end: new Date(),
    }),
  },
  {
    label: 'Last 30 days',
    value: '30d',
    description: 'Last 30 days',
    getDateRange: () => ({
      start: subDays(new Date(), 30),
      end: new Date(),
    }),
  },
  {
    label: 'Month to date',
    value: 'mtd',
    description: 'From the start of this calendar month',
    getDateRange: () => ({
      start: startOfMonth(new Date()),
      end: new Date(),
    }),
  },
  {
    label: 'Last calendar month',
    value: 'last_month',
    description: 'Previous calendar month',
    getDateRange: () => {
      const prev = subMonths(new Date(), 1);
      return {
        start: startOfMonth(prev),
        // Half-open end: first instant of current month
        end: startOfMonth(new Date()),
      };
    },
  },
];

/** Compare optional dates by timestamp; null/undefined only match the same sentinel. */
function sameDate(a?: Date | null, b?: Date | null): boolean {
  return a === b || (a != null && b != null && a.getTime() === b.getTime());
}

/**
 * Human-readable description of the currently-applied range, for display
 * outside the modal (filter buttons/chips) and inside it (the "currently
 * applied" banner). Presets are shown by their friendly label; custom
 * ranges are formatted as dates (with times included only when a boundary
 * isn't at midnight, so plain day ranges stay compact).
 */
export function formatAppliedRange(
  startDate: Date | null | undefined,
  endDate: Date | null | undefined,
  presetValue: string | null | undefined,
  presets: TimePreset[] = DEFAULT_TIME_PRESETS,
): string | null {
  if (presetValue) {
    const preset = presets.find((p) => p.value === presetValue);
    if (preset) return preset.label;
  }
  if (!startDate && !endDate) return null;

  const isMidnight = (d: Date) =>
    d.getHours() === 0 && d.getMinutes() === 0 && d.getSeconds() === 0 && d.getMilliseconds() === 0;
  const showTime = [startDate, endDate].some((d) => d != null && !isMidnight(d));
  const fmt = showTime ? 'MMM d, yyyy HH:mm' : 'MMM d, yyyy';

  if (startDate && endDate) return `${format(startDate, fmt)} \u2013 ${format(endDate, fmt)}`;
  if (startDate) return `From ${format(startDate, fmt)}`;
  return `Until ${format(endDate!, fmt)}`;
}

/**
 * TimeRangeModal — Advanced Time Range Selection
 * Provides both preset time ranges and custom date/time selection in a modal dialog.
 */
export function TimeRangeModal({
  open,
  onClose,
  startDate,
  endDate,
  activePreset = null,
  onApply,
  presets = DEFAULT_TIME_PRESETS,
}: TimeRangeModalProps) {
  const [selectedPreset, setSelectedPreset] = useState<string | null>(null);
  const [customStartDate, setCustomStartDate] = useState<Date | null>(startDate || null);
  const [customEndDate, setCustomEndDate] = useState<Date | null>(endDate || null);
  const [mode, setMode] = useState<'preset' | 'custom'>('preset');
  const timePresets = presets;

  // Reset state when modal opens (null = not yet synced), restoring whichever
  // tab/selection matches what's actually applied — so reopening the dialog
  // shows the currently-in-effect range instead of always defaulting to
  // "Quick Select" with nothing highlighted.
  // Compare dates by timestamp so new Date instances with the same value don't retrigger.
  const [resetSnapshot, setResetSnapshot] = useState<{
    open: boolean;
    startDate?: Date | null;
    endDate?: Date | null;
    activePreset?: string | null;
  } | null>(null);
  if (
    resetSnapshot === null ||
    open !== resetSnapshot.open ||
    !sameDate(startDate, resetSnapshot.startDate) ||
    !sameDate(endDate, resetSnapshot.endDate) ||
    activePreset !== resetSnapshot.activePreset
  ) {
    setResetSnapshot({ open, startDate, endDate, activePreset });
    if (open) {
      setCustomStartDate(startDate || null);
      setCustomEndDate(endDate || null);
      if (activePreset && timePresets.some((p) => p.value === activePreset)) {
        setSelectedPreset(activePreset);
        setMode('preset');
      } else if (startDate || endDate) {
        setSelectedPreset(null);
        setMode('custom');
      } else {
        setSelectedPreset(null);
        setMode('preset');
      }
    }
  }

  const appliedRangeLabel = formatAppliedRange(startDate, endDate, activePreset, timePresets);

  // Handle preset selection
  const handlePresetSelect = (preset: TimePreset) => {
    setSelectedPreset(preset.value);
    setMode('preset');
  };

  // Handle custom mode
  const handleCustomMode = () => {
    setMode('custom');
    setSelectedPreset(null);
  };

  // Handle apply
  const handleApply = () => {
    if (mode === 'preset' && selectedPreset) {
      const preset = timePresets.find((p) => p.value === selectedPreset);
      if (preset) {
        const { start, end } = preset.getDateRange();
        onApply(start, end, selectedPreset);
      }
    } else if (mode === 'custom') {
      onApply(customStartDate, customEndDate);
    }
    onClose();
  };

  // Handle clear
  const handleClear = () => {
    onApply(null, null);
    onClose();
  };

  // Validation
  const isValidCustomRange = () => {
    if (!customStartDate || !customEndDate) return true; // Allow partial dates
    return isBefore(customStartDate, customEndDate);
  };

  const hasSelection = () => {
    return (
      (mode === 'preset' && selectedPreset) ||
      (mode === 'custom' && (customStartDate || customEndDate))
    );
  };

  return (
    <LocalizationProvider dateAdapter={AdapterDateFns}>
      <Dialog
        open={open}
        onClose={onClose}
        maxWidth="md"
        fullWidth
        PaperProps={{
          sx: { minHeight: 500 },
        }}
      >
        <DialogTitle
          sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}
        >
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
            <AccessTime color="primary" />
            <Typography variant="h6">Select Time Range</Typography>
          </Box>
          <IconButton onClick={onClose} size="small">
            <Close />
          </IconButton>
        </DialogTitle>

        <DialogContent sx={{ pb: 1 }}>
          {/* Currently applied — always visible, independent of which tab is open */}
          <Box
            sx={{
              mb: 3,
              p: 1.5,
              borderRadius: 1,
              bgcolor: 'action.hover',
              display: 'flex',
              alignItems: 'baseline',
              gap: 1,
              flexWrap: 'wrap',
            }}
          >
            <Typography variant="caption" color="text.secondary" sx={{ textTransform: 'uppercase', letterSpacing: 0.5 }}>
              Currently applied
            </Typography>
            <Typography variant="body2" fontWeight={600}>
              {appliedRangeLabel ?? 'All time'}
            </Typography>
          </Box>

          {/* Mode Selection */}
          <Box sx={{ mb: 3 }}>
            <Stack direction="row" spacing={1}>
              <Button
                variant={mode === 'preset' ? 'contained' : 'outlined'}
                onClick={() => setMode('preset')}
                startIcon={<AccessTime />}
                size="small"
              >
                Quick Select
              </Button>
              <Button
                variant={mode === 'custom' ? 'contained' : 'outlined'}
                onClick={handleCustomMode}
                startIcon={<CalendarToday />}
                size="small"
              >
                Custom Range
              </Button>
            </Stack>
          </Box>

          <Divider sx={{ mb: 3 }} />

          {/* Preset Mode */}
          {mode === 'preset' && (
            <Box>
              <Typography variant="subtitle1" gutterBottom sx={{ fontWeight: 600 }}>
                Choose a time range:
              </Typography>
              <Box
                sx={{
                  display: 'grid',
                  gridTemplateColumns: 'repeat(auto-fit, minmax(200px, 1fr))',
                  gap: 2,
                }}
              >
                {timePresets.map((preset) => (
                  <Chip
                    key={preset.value}
                    label={preset.label}
                    onClick={() => handlePresetSelect(preset)}
                    color={selectedPreset === preset.value ? 'primary' : 'default'}
                    variant={selectedPreset === preset.value ? 'filled' : 'outlined'}
                    sx={{
                      height: 48,
                      fontSize: '0.875rem',
                      '&:hover': {
                        backgroundColor:
                          selectedPreset === preset.value ? 'primary.dark' : 'action.hover',
                      },
                    }}
                  />
                ))}
              </Box>

              {selectedPreset && (
                <Alert severity="info" sx={{ mt: 2 }}>
                  <strong>Will apply:</strong>{' '}
                  {timePresets.find((p) => p.value === selectedPreset)?.description}
                  <br />
                  <strong>From:</strong>{' '}
                  {format(
                    timePresets.find((p) => p.value === selectedPreset)!.getDateRange().start,
                    'MMM dd, yyyy HH:mm',
                  )}
                  <br />
                  <strong>To:</strong>{' '}
                  {format(
                    timePresets.find((p) => p.value === selectedPreset)!.getDateRange().end,
                    'MMM dd, yyyy HH:mm',
                  )}
                </Alert>
              )}
            </Box>
          )}

          {/* Custom Mode */}
          {mode === 'custom' && (
            <Box>
              <Typography variant="subtitle1" gutterBottom sx={{ fontWeight: 600 }}>
                Select custom date and time range:
              </Typography>

              <Stack spacing={3}>
                <Box>
                  <Typography variant="body2" gutterBottom color="text.secondary">
                    Start Date & Time
                  </Typography>
                  <DateTimePicker
                    label="From"
                    value={customStartDate}
                    onChange={setCustomStartDate}
                    slotProps={{
                      textField: {
                        fullWidth: true,
                        size: 'medium',
                      },
                    }}
                  />
                </Box>

                <Box>
                  <Typography variant="body2" gutterBottom color="text.secondary">
                    End Date & Time
                  </Typography>
                  <DateTimePicker
                    label="To"
                    value={customEndDate}
                    onChange={setCustomEndDate}
                    slotProps={{
                      textField: {
                        fullWidth: true,
                        size: 'medium',
                      },
                    }}
                  />
                </Box>
              </Stack>

              {!isValidCustomRange() && (
                <Alert severity="error" sx={{ mt: 2 }}>
                  End date must be after start date
                </Alert>
              )}

              {customStartDate && customEndDate && isValidCustomRange() && (
                <Alert severity="success" sx={{ mt: 2 }}>
                  <strong>Will apply:</strong>
                  <br />
                  <strong>From:</strong> {format(customStartDate, 'MMM dd, yyyy HH:mm')}
                  <br />
                  <strong>To:</strong> {format(customEndDate, 'MMM dd, yyyy HH:mm')}
                </Alert>
              )}
            </Box>
          )}
        </DialogContent>

        <DialogActions sx={{ px: 3, pb: 2 }}>
          <Button onClick={handleClear} color="error" variant="outlined">
            Clear Filter
          </Button>
          <Box sx={{ flex: 1 }} />
          <Button onClick={onClose} color="inherit">
            Cancel
          </Button>
          <Button
            onClick={handleApply}
            variant="contained"
            disabled={!hasSelection() || (mode === 'custom' && !isValidCustomRange())}
          >
            Apply Filter
          </Button>
        </DialogActions>
      </Dialog>
    </LocalizationProvider>
  );
}
