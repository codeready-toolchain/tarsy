import { useState, useEffect } from 'react';
import {
  Dialog,
  DialogTitle,
  DialogContent,
  DialogActions,
  Button,
  Box,
  Typography,
  TextField,
  RadioGroup,
  Radio,
  FormControlLabel,
  FormControl,
  FormLabel,
  IconButton,
} from '@mui/material';
import { Close, CheckCircleOutline } from '@mui/icons-material';

export interface ResolveModalProps {
  open: boolean;
  onClose: () => void;
  onResolve: (reason: string, note?: string) => void;
  loading?: boolean;
}

export function ResolveModal({ open, onClose, onResolve, loading }: ResolveModalProps) {
  const [reason, setReason] = useState<string>('');
  const [note, setNote] = useState('');

  useEffect(() => {
    if (open) {
      setReason('');
      setNote('');
    }
  }, [open]);

  const handleResolve = () => {
    if (!reason) return;
    onResolve(reason, note.trim() || undefined);
  };

  return (
    <Dialog open={open} onClose={onClose} maxWidth="sm" fullWidth>
      <DialogTitle sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          <CheckCircleOutline color="success" />
          <Typography variant="h6">Resolve Session</Typography>
        </Box>
        <IconButton onClick={onClose} size="small">
          <Close />
        </IconButton>
      </DialogTitle>

      <DialogContent sx={{ pb: 1 }}>
        <FormControl component="fieldset" sx={{ mb: 2, mt: 1 }}>
          <FormLabel component="legend" sx={{ mb: 1, fontWeight: 600 }}>
            Resolution reason
          </FormLabel>
          <RadioGroup value={reason} onChange={(e) => setReason(e.target.value)}>
            <FormControlLabel
              value="actioned"
              control={<Radio />}
              label={
                <Box>
                  <Typography variant="body1" fontWeight={500}>Actioned</Typography>
                  <Typography variant="body2" color="text.secondary">
                    The investigation led to a concrete action (fix applied, ticket created, etc.)
                  </Typography>
                </Box>
              }
              sx={{ mb: 1, alignItems: 'flex-start', '& .MuiRadio-root': { mt: 0.5 } }}
            />
            <FormControlLabel
              value="dismissed"
              control={<Radio />}
              label={
                <Box>
                  <Typography variant="body1" fontWeight={500}>Dismissed</Typography>
                  <Typography variant="body2" color="text.secondary">
                    No action needed (false positive, already resolved, not relevant, etc.)
                  </Typography>
                </Box>
              }
              sx={{ alignItems: 'flex-start', '& .MuiRadio-root': { mt: 0.5 } }}
            />
          </RadioGroup>
        </FormControl>

        <TextField
          label="Note (optional)"
          placeholder="e.g., Applied fix from runbook, ticket INFRA-1234"
          value={note}
          onChange={(e) => setNote(e.target.value)}
          multiline
          minRows={2}
          maxRows={4}
          fullWidth
        />
      </DialogContent>

      <DialogActions sx={{ px: 3, pb: 2 }}>
        <Button onClick={onClose} color="inherit" disabled={loading}>
          Cancel
        </Button>
        <Button
          onClick={handleResolve}
          variant="contained"
          color="success"
          disabled={!reason || loading}
        >
          {loading ? 'Resolving...' : 'Resolve'}
        </Button>
      </DialogActions>
    </Dialog>
  );
}
