/**
 * TimeRangeModal — preset and custom time range selection dialog.
 *
 * Ported from old dashboard's TimeRangeModal.tsx.
 * Provides both preset time ranges (10m, 1h, 12h, 1d, 7d, 30d) and
 * custom date/time selection in a modal dialog, matching old dashboard UX.
 */

import { useState, useEffect } from 'react';
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
import { format, subMinutes, subHours, subDays, isBefore } from 'date-fns';

export interface TimeRangeModalProps {
  open: boolean;
  onClose: () => void;
  startDate?: Date | null;
  endDate?: Date | null;
  onApply: (startDate: Date | null, endDate: Date | null, preset?: string) => void;
}

interface TimePreset {
  label: string;
  value: string;
  description: string;
  getDateRange: () => { start: Date; end: Date };
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
  onApply,
}: TimeRangeModalProps) {
  const [selectedPreset, setSelectedPreset] = useState<string | null>(null);
  const [customStartDate, setCustomStartDate] = useState<Date | null>(startDate || null);
  const [customEndDate, setCustomEndDate] = useState<Date | null>(endDate || null);
  const [mode, setMode] = useState<'preset' | 'custom'>('preset');

  // Time presets — matching old dashboard
  const timePresets: TimePreset[] = [
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

  // Reset state when modal opens
  useEffect(() => {
    if (open) {
      setCustomStartDate(startDate || null);
      setCustomEndDate(endDate || null);
      setSelectedPreset(null);
      setMode('preset');
    }
  }, [open, startDate, endDate]);

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
                  <strong>Selected:</strong>{' '}
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
                  <strong>Custom Range:</strong>
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
