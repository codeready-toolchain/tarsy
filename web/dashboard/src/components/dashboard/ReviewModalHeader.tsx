import type { ReactNode } from 'react';
import { DialogTitle, Box, Typography, IconButton, Chip } from '@mui/material';
import { Close, PersonOutline, EditOutlined } from '@mui/icons-material';
import { timeAgo } from '../../utils/format.ts';

interface ReviewModalHeaderProps {
  icon: ReactNode;
  title: string;
  feedbackEdited?: boolean;
  feedbackEditedBy?: string | null;
  feedbackEditedAt?: string | null;
  assignee?: string | null;
  onClose: () => void;
}

export function ReviewModalHeader({ icon, title, feedbackEdited, feedbackEditedBy, feedbackEditedAt, assignee, onClose }: ReviewModalHeaderProps) {
  return (
    <DialogTitle sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
      <Box>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          {icon}
          <Typography variant="h6">{title}</Typography>
          {feedbackEdited && (
            <Chip
              icon={<EditOutlined sx={{ fontSize: 14 }} />}
              label="Edited"
              size="small"
              variant="outlined"
              color="info"
              sx={{ height: 22, '& .MuiChip-label': { px: 0.5, fontSize: '0.7rem' } }}
            />
          )}
        </Box>
        {assignee && (
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5, mt: 0.5, ml: 0.5 }}>
            <PersonOutline sx={{ fontSize: 16, color: 'text.secondary' }} />
            <Typography variant="body2" color="text.secondary">
              {assignee}
            </Typography>
          </Box>
        )}
        {feedbackEditedBy && (
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5, mt: 0.5, ml: 0.5 }}>
            <EditOutlined sx={{ fontSize: 16, color: 'text.secondary' }} />
            <Typography variant="body2" color="text.secondary">
              Edited by {feedbackEditedBy}{feedbackEditedAt ? `, ${timeAgo(feedbackEditedAt)}` : ''}
            </Typography>
          </Box>
        )}
      </Box>
      <IconButton onClick={onClose} size="small" sx={{ mt: 0.5 }}>
        <Close />
      </IconButton>
    </DialogTitle>
  );
}
