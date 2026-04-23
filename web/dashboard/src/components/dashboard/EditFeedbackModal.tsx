import { useState, useEffect } from 'react';
import {
  Dialog,
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
  Divider,
  Alert,
} from '@mui/material';
import { RateReview, ThumbUp, ThumbsUpDown, ThumbDown } from '@mui/icons-material';
import { ReviewModalHeader } from './ReviewModalHeader.tsx';
import ReactMarkdown from 'react-markdown';
import { remarkPlugins, executiveSummaryMarkdownStyles } from '../../utils/markdownComponents.tsx';
import { QUALITY_RATING } from '../../types/api.ts';

export interface EditFeedbackModalProps {
  open: boolean;
  initialQualityRating: string;
  initialActionTaken: string;
  initialInvestigationFeedback: string;
  onClose: () => void;
  onSave: (qualityRating: string, actionTaken: string, investigationFeedback: string) => void;
  loading?: boolean;
  executiveSummary?: string | null;
  assignee?: string | null;
  feedbackEdited?: boolean;
  feedbackEditedBy?: string | null;
  feedbackEditedAt?: string | null;
  error?: string | null;
}

export function EditFeedbackModal({
  open,
  initialQualityRating,
  initialActionTaken,
  initialInvestigationFeedback,
  onClose,
  onSave,
  loading,
  executiveSummary,
  assignee,
  feedbackEdited,
  feedbackEditedBy,
  feedbackEditedAt,
  error,
}: EditFeedbackModalProps) {
  const [qualityRating, setQualityRating] = useState('');
  const [actionTaken, setActionTaken] = useState('');
  const [investigationFeedback, setInvestigationFeedback] = useState('');

  useEffect(() => {
    if (open) {
      setQualityRating(initialQualityRating);
      setActionTaken(initialActionTaken);
      setInvestigationFeedback(initialInvestigationFeedback);
    }
  }, [open, initialQualityRating, initialActionTaken, initialInvestigationFeedback]);

  const handleSave = () => {
    onSave(qualityRating, actionTaken.trim(), investigationFeedback.trim());
  };

  const changed =
    qualityRating !== initialQualityRating ||
    actionTaken.trim() !== initialActionTaken.trim() ||
    investigationFeedback.trim() !== initialInvestigationFeedback.trim();

  return (
    <Dialog open={open} onClose={onClose} maxWidth="sm" fullWidth disableScrollLock>
      <ReviewModalHeader
        icon={<RateReview color="primary" />}
        title="Edit Review Feedback"
        feedbackEdited={feedbackEdited}
        feedbackEditedBy={feedbackEditedBy}
        feedbackEditedAt={feedbackEditedAt}
        assignee={assignee}
        onClose={onClose}
      />

      <DialogContent sx={{ pb: 1 }}>
        {executiveSummary && (
          <>
            <Box
              sx={(theme) => ({
                mt: 1,
                mb: 2,
                p: 1.5,
                borderRadius: 1,
                bgcolor: 'action.hover',
                ...executiveSummaryMarkdownStyles(theme),
              })}
            >
              <Typography variant="caption" color="text.secondary" fontWeight={600} sx={{ mb: 0.5, display: 'block' }}>
                Executive Summary
              </Typography>
              <ReactMarkdown remarkPlugins={remarkPlugins} skipHtml>{executiveSummary}</ReactMarkdown>
            </Box>
            <Divider sx={{ mb: 1 }} />
          </>
        )}
        <FormControl component="fieldset" sx={{ mb: 2, mt: 1 }}>
          <FormLabel component="legend" sx={{ mb: 1, fontWeight: 600 }}>
            Investigation quality
          </FormLabel>
          <RadioGroup value={qualityRating} onChange={(e) => setQualityRating(e.target.value)}>
            <FormControlLabel
              value={QUALITY_RATING.ACCURATE}
              control={<Radio sx={{ color: 'success.main', '&.Mui-checked': { color: 'success.main' } }} />}
              label={
                <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.75 }}>
                  <ThumbUp sx={{ fontSize: 16, color: 'success.main' }} />
                  <Typography variant="body2" fontWeight={500}>Accurate</Typography>
                </Box>
              }
              sx={{ mb: 0.5 }}
            />
            <FormControlLabel
              value={QUALITY_RATING.PARTIALLY_ACCURATE}
              control={<Radio sx={{ color: 'warning.main', '&.Mui-checked': { color: 'warning.main' } }} />}
              label={
                <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.75 }}>
                  <ThumbsUpDown sx={{ fontSize: 16, color: 'warning.main' }} />
                  <Typography variant="body2" fontWeight={500}>Partially Accurate</Typography>
                </Box>
              }
              sx={{ mb: 0.5 }}
            />
            <FormControlLabel
              value={QUALITY_RATING.INACCURATE}
              control={<Radio sx={{ color: 'error.main', '&.Mui-checked': { color: 'error.main' } }} />}
              label={
                <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.75 }}>
                  <ThumbDown sx={{ fontSize: 16, color: 'error.main' }} />
                  <Typography variant="body2" fontWeight={500}>Inaccurate</Typography>
                </Box>
              }
            />
          </RadioGroup>
        </FormControl>

        <TextField
          label="Action taken"
          placeholder="Note about taken action, e.g., applied fix from runbook, ticket INFRA-1234"
          value={actionTaken}
          onChange={(e) => setActionTaken(e.target.value)}
          multiline
          minRows={2}
          maxRows={4}
          fullWidth
          sx={{ mb: 2 }}
        />

        <TextField
          label="Investigation feedback"
          placeholder="e.g., Missed the root cause, focused on wrong service"
          value={investigationFeedback}
          onChange={(e) => setInvestigationFeedback(e.target.value)}
          multiline
          minRows={2}
          maxRows={4}
          fullWidth
        />
      </DialogContent>

      {error && (
        <Alert severity="error" sx={{ mx: 3, mb: 1 }}>{error}</Alert>
      )}

      <DialogActions sx={{ px: 3, pb: 2 }}>
        <Button onClick={onClose} color="inherit" disabled={loading}>
          Cancel
        </Button>
        <Button
          onClick={handleSave}
          variant="contained"
          disabled={!changed || !qualityRating || loading}
        >
          {loading ? 'Saving...' : 'Save'}
        </Button>
      </DialogActions>
    </Dialog>
  );
}
