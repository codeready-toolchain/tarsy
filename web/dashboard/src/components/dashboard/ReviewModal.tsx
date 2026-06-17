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
  Collapse,
} from '@mui/material';
import { CheckCircleOutline, RateReview, ThumbUp, ThumbsUpDown, ThumbDown, DoneAll } from '@mui/icons-material';
import { ReviewModalHeader } from './ReviewModalHeader.tsx';
import ReactMarkdown from 'react-markdown';
import { remarkPlugins, executiveSummaryMarkdownStyles } from '../../utils/markdownComponents.tsx';
import { QUALITY_RATING, REVIEW_SELECTION, type ReviewSelection } from '../../types/api.ts';

export type ReviewModalMode = 'complete' | 'edit';

export interface ReviewModalProps {
  open: boolean;
  mode: ReviewModalMode;
  onClose: () => void;
  onSubmit: (qualityRating: ReviewSelection, actionTaken: string, investigationFeedback: string) => void;
  loading?: boolean;
  error?: string | null;
  title?: string;
  executiveSummary?: string | null;
  assignee?: string | null;
  feedbackEdited?: boolean;
  feedbackEditedBy?: string | null;
  feedbackEditedAt?: string | null;
  initialRating?: string;
  initialActionTaken?: string;
  initialInvestigationFeedback?: string;
}

export function ReviewModal({
  open,
  mode,
  onClose,
  onSubmit,
  loading,
  error,
  title,
  executiveSummary,
  assignee,
  feedbackEdited,
  feedbackEditedBy,
  feedbackEditedAt,
  initialRating,
  initialActionTaken = '',
  initialInvestigationFeedback = '',
}: ReviewModalProps) {
  const isEdit = mode === 'edit';
  const optionalSuffix = isEdit ? '' : ' (optional)';

  const [qualityRating, setQualityRating] = useState<ReviewSelection | ''>('');
  const [actionTaken, setActionTaken] = useState('');
  const [investigationFeedback, setInvestigationFeedback] = useState('');
  const [showActionTaken, setShowActionTaken] = useState(false);

  useEffect(() => {
    if (open) {
      setQualityRating((initialRating as ReviewSelection) || (isEdit ? '' : QUALITY_RATING.ACCURATE));
      setActionTaken(initialActionTaken);
      setInvestigationFeedback(initialInvestigationFeedback);
      setShowActionTaken(!!initialActionTaken.trim());
    }
  }, [open, isEdit, initialRating, initialActionTaken, initialInvestigationFeedback]);

  const isAcknowledge = qualityRating === REVIEW_SELECTION.ACKNOWLEDGE;

  const handleSubmit = () => {
    if (!qualityRating) return;
    onSubmit(
      qualityRating,
      isAcknowledge ? '' : actionTaken.trim(),
      isAcknowledge ? '' : investigationFeedback.trim(),
    );
  };

  const changed = !isEdit || (
    qualityRating !== (initialRating ?? '') ||
    actionTaken.trim() !== initialActionTaken.trim() ||
    investigationFeedback.trim() !== initialInvestigationFeedback.trim()
  );

  const headerIcon = isEdit
    ? <RateReview color="primary" />
    : <CheckCircleOutline color="success" />;
  const headerTitle = isEdit
    ? 'Edit Review Feedback'
    : (title ?? 'Complete Review');

  const submitLabel = isAcknowledge
    ? 'Acknowledge'
    : (isEdit ? 'Save Changes' : 'Complete Review');
  const loadingLabel = isAcknowledge
    ? 'Acknowledging...'
    : (isEdit ? 'Saving...' : 'Completing...');

  return (
    <Dialog open={open} onClose={onClose} maxWidth="sm" fullWidth disableScrollLock>
      <ReviewModalHeader
        icon={headerIcon}
        title={isEdit ? headerTitle : (title ?? headerTitle)}
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
          <RadioGroup value={qualityRating} onChange={(e) => setQualityRating(e.target.value as ReviewSelection)}>
            <FormControlLabel
              value={QUALITY_RATING.ACCURATE}
              control={<Radio sx={{ color: 'success.main', '&.Mui-checked': { color: 'success.main' } }} />}
              label={
                <Box>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.75 }}>
                    <ThumbUp sx={{ fontSize: 16, color: 'success.main' }} />
                    <Typography variant="body1" fontWeight={500}>Accurate</Typography>
                  </Box>
                  <Typography variant="body2" color="text.secondary">
                    The investigation correctly identified the issue and root cause
                  </Typography>
                </Box>
              }
              sx={{ mb: 1, alignItems: 'flex-start', '& .MuiRadio-root': { mt: 0.5 } }}
            />
            <FormControlLabel
              value={QUALITY_RATING.PARTIALLY_ACCURATE}
              control={<Radio sx={{ color: 'warning.main', '&.Mui-checked': { color: 'warning.main' } }} />}
              label={
                <Box>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.75 }}>
                    <ThumbsUpDown sx={{ fontSize: 16, color: 'warning.main' }} />
                    <Typography variant="body1" fontWeight={500}>Partially Accurate</Typography>
                  </Box>
                  <Typography variant="body2" color="text.secondary">
                    Some findings were correct but the investigation missed key aspects
                  </Typography>
                </Box>
              }
              sx={{ mb: 1, alignItems: 'flex-start', '& .MuiRadio-root': { mt: 0.5 } }}
            />
            <FormControlLabel
              value={QUALITY_RATING.INACCURATE}
              control={<Radio sx={{ color: 'error.main', '&.Mui-checked': { color: 'error.main' } }} />}
              label={
                <Box>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.75 }}>
                    <ThumbDown sx={{ fontSize: 16, color: 'error.main' }} />
                    <Typography variant="body1" fontWeight={500}>Inaccurate</Typography>
                  </Box>
                  <Typography variant="body2" color="text.secondary">
                    The investigation was wrong or misleading
                  </Typography>
                </Box>
              }
              sx={{ mb: 1, alignItems: 'flex-start', '& .MuiRadio-root': { mt: 0.5 } }}
            />
            <FormControlLabel
              value={REVIEW_SELECTION.ACKNOWLEDGE}
              control={<Radio />}
              label={
                <Box>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.75 }}>
                    <DoneAll sx={{ fontSize: 16, color: 'text.secondary' }} />
                    <Typography variant="body1" fontWeight={500}>Acknowledge</Typography>
                  </Box>
                  <Typography variant="body2" color="text.secondary">
                    I've reviewed this but won't judge investigation quality
                  </Typography>
                </Box>
              }
              sx={{ alignItems: 'flex-start', '& .MuiRadio-root': { mt: 0.5 } }}
            />
          </RadioGroup>
        </FormControl>

        {isEdit && isAcknowledge && initialRating && initialRating !== REVIEW_SELECTION.ACKNOWLEDGE && (
          <Alert severity="info" sx={{ mb: 2 }}>
            Switching to Acknowledge will remove the current quality rating.
          </Alert>
        )}

        <Collapse in={!isAcknowledge}>
          <TextField
            label={`Investigation feedback${optionalSuffix}`}
            placeholder="e.g., Missed the root cause, focused on wrong service"
            helperText="Helps TARSy learn — tell it what the investigation got right or wrong to improve future analyses"
            value={investigationFeedback}
            onChange={(e) => setInvestigationFeedback(e.target.value)}
            multiline
            minRows={2}
            maxRows={4}
            fullWidth
            sx={{ mb: 1 }}
          />

          <Typography
            variant="body2"
            color="primary"
            onClick={() => setShowActionTaken((v) => !v)}
            sx={{ cursor: 'pointer', mb: 1, display: 'inline-block', '&:hover': { textDecoration: 'underline' } }}
          >
            {showActionTaken ? '− Hide action taken' : '+ Add action taken note'}
          </Typography>
          <Collapse in={showActionTaken}>
            <TextField
              label={`Action taken${optionalSuffix}`}
              placeholder="e.g., applied fix from runbook, created ticket INFRA-1234"
              helperText="For your team's records only — documents what you did to resolve the alert (not used by TARSy for learning)"
              value={actionTaken}
              onChange={(e) => setActionTaken(e.target.value)}
              multiline
              minRows={2}
              maxRows={4}
              fullWidth
            />
          </Collapse>
        </Collapse>
      </DialogContent>

      {error && (
        <Alert severity="error" sx={{ mx: 3, mb: 1 }}>{error}</Alert>
      )}

      <DialogActions sx={{ px: 3, pb: 2 }}>
        <Button onClick={onClose} color="inherit" disabled={loading}>
          Cancel
        </Button>
        <Button
          onClick={handleSubmit}
          variant="contained"
          color={isAcknowledge ? 'primary' : (isEdit ? undefined : 'success')}
          disabled={!changed || !qualityRating || loading}
        >
          {loading ? loadingLabel : submitLabel}
        </Button>
      </DialogActions>
    </Dialog>
  );
}
