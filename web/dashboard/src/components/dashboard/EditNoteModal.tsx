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
  IconButton,
} from '@mui/material';
import { Close, StickyNote2Outlined } from '@mui/icons-material';

export interface EditNoteModalProps {
  open: boolean;
  initialNote: string;
  onClose: () => void;
  onSave: (note: string) => void;
  loading?: boolean;
}

export function EditNoteModal({ open, initialNote, onClose, onSave, loading }: EditNoteModalProps) {
  const [note, setNote] = useState('');

  useEffect(() => {
    if (open) {
      setNote(initialNote);
    }
  }, [open, initialNote]);

  const handleSave = () => {
    onSave(note.trim());
  };

  const changed = note.trim() !== initialNote.trim();

  return (
    <Dialog open={open} onClose={onClose} maxWidth="sm" fullWidth>
      <DialogTitle sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          <StickyNote2Outlined color="primary" />
          <Typography variant="h6">Resolution Note</Typography>
        </Box>
        <IconButton onClick={onClose} size="small">
          <Close />
        </IconButton>
      </DialogTitle>

      <DialogContent sx={{ pb: 1 }}>
        <TextField
          label="Note"
          placeholder="e.g., Applied fix from runbook, ticket INFRA-1234"
          value={note}
          onChange={(e) => setNote(e.target.value)}
          multiline
          minRows={3}
          maxRows={6}
          fullWidth
          autoFocus
          sx={{ mt: 1 }}
        />
      </DialogContent>

      <DialogActions sx={{ px: 3, pb: 2 }}>
        <Button onClick={onClose} color="inherit" disabled={loading}>
          Cancel
        </Button>
        <Button
          onClick={handleSave}
          variant="contained"
          disabled={!changed || loading}
        >
          {loading ? 'Saving...' : 'Save'}
        </Button>
      </DialogActions>
    </Dialog>
  );
}
