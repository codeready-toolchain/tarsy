import { TextField } from '@mui/material';
import {
  MAX_CANCEL_REASON_LENGTH,
  cancelReasonRuneCount,
  isCancelReasonOverLimit,
} from '../../constants/sessionStatus.ts';

interface CancelReasonFieldProps {
  value: string;
  onChange: (value: string) => void;
  disabled?: boolean;
}

/**
 * Optional multiline cancel reason with a remaining-rune counter (max 500).
 */
export function CancelReasonField({ value, onChange, disabled }: CancelReasonFieldProps) {
  const remaining = MAX_CANCEL_REASON_LENGTH - cancelReasonRuneCount(value);
  const overLimit = isCancelReasonOverLimit(value);

  return (
    <TextField
      label="Reason (optional)"
      multiline
      minRows={2}
      fullWidth
      value={value}
      onChange={(e) => onChange(e.target.value)}
      disabled={disabled}
      error={overLimit}
      helperText={`${remaining} remaining`}
      sx={{ mt: 2 }}
    />
  );
}
