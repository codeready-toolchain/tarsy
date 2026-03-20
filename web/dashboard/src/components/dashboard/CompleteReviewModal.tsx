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

export interface CompleteReviewModalProps {
  open: boolean;
  onClose: () => void;
  onComplete: (qualityRating: string, actionTaken?: string, investigationFeedback?: string) => void;
  loading?: boolean;
  title?: string;
}

export function CompleteReviewModal({ open, onClose, onComplete, loading, title }: CompleteReviewModalProps) {
  const [qualityRating, setQualityRating] = useState<string>('');
  const [actionTaken, setActionTaken] = useState('');
  const [investigationFeedback, setInvestigationFeedback] = useState('');

  useEffect(() => {
    if (open) {
      setQualityRating('');
      setActionTaken('');
      setInvestigationFeedback('');
    }
  }, [open]);

  const handleComplete = () => {
    if (!qualityRating) return;
    onComplete(
      qualityRating,
      actionTaken.trim() || undefined,
      investigationFeedback.trim() || undefined,
    );
  };

  return (
    <Dialog open={open} onClose={onClose} maxWidth="sm" fullWidth>
      <DialogTitle sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          <CheckCircleOutline color="success" />
          <Typography variant="h6">{title ?? 'Complete Review'}</Typography>
        </Box>
        <IconButton onClick={onClose} size="small">
          <Close />
        </IconButton>
      </DialogTitle>

      <DialogContent sx={{ pb: 1 }}>
        <FormControl component="fieldset" sx={{ mb: 2, mt: 1 }}>
          <FormLabel component="legend" sx={{ mb: 1, fontWeight: 600 }}>
            Investigation quality
          </FormLabel>
          <RadioGroup value={qualityRating} onChange={(e) => setQualityRating(e.target.value)}>
            <FormControlLabel
              value="accurate"
              control={<Radio sx={{ color: 'success.main', '&.Mui-checked': { color: 'success.main' } }} />}
              label={
                <Box>
                  <Typography variant="body1" fontWeight={500}>Accurate</Typography>
                  <Typography variant="body2" color="text.secondary">
                    The investigation correctly identified the issue and root cause
                  </Typography>
                </Box>
              }
              sx={{ mb: 1, alignItems: 'flex-start', '& .MuiRadio-root': { mt: 0.5 } }}
            />
            <FormControlLabel
              value="partially_accurate"
              control={<Radio sx={{ color: 'warning.main', '&.Mui-checked': { color: 'warning.main' } }} />}
              label={
                <Box>
                  <Typography variant="body1" fontWeight={500}>Partially Accurate</Typography>
                  <Typography variant="body2" color="text.secondary">
                    Some findings were correct but the investigation missed key aspects
                  </Typography>
                </Box>
              }
              sx={{ mb: 1, alignItems: 'flex-start', '& .MuiRadio-root': { mt: 0.5 } }}
            />
            <FormControlLabel
              value="inaccurate"
              control={<Radio sx={{ color: 'error.main', '&.Mui-checked': { color: 'error.main' } }} />}
              label={
                <Box>
                  <Typography variant="body1" fontWeight={500}>Inaccurate</Typography>
                  <Typography variant="body2" color="text.secondary">
                    The investigation was wrong or misleading
                  </Typography>
                </Box>
              }
              sx={{ alignItems: 'flex-start', '& .MuiRadio-root': { mt: 0.5 } }}
            />
          </RadioGroup>
        </FormControl>

        <TextField
          label="Action taken (optional)"
          placeholder="e.g., Applied fix from runbook, ticket INFRA-1234"
          value={actionTaken}
          onChange={(e) => setActionTaken(e.target.value)}
          multiline
          minRows={2}
          maxRows={4}
          fullWidth
          sx={{ mb: 2 }}
        />

        <TextField
          label="Investigation feedback (optional)"
          placeholder="e.g., Missed the root cause, focused on wrong service"
          value={investigationFeedback}
          onChange={(e) => setInvestigationFeedback(e.target.value)}
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
          onClick={handleComplete}
          variant="contained"
          color="success"
          disabled={!qualityRating || loading}
        >
          {loading ? 'Completing...' : 'Complete Review'}
        </Button>
      </DialogActions>
    </Dialog>
  );
}
