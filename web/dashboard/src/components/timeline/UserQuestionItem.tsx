import { memo } from 'react';
import { Box, Typography, alpha } from '@mui/material';
import { AccountCircle } from '@mui/icons-material';
import type { FlowItem } from '../../utils/timelineParser';

interface UserQuestionItemProps {
  item: FlowItem;
}

/**
 * UserQuestionItem - renders user_question timeline events.
 * Circular avatar with user message box, matching old dashboard style.
 */
function UserQuestionItem({ item }: UserQuestionItemProps) {
  const author = (item.metadata?.author as string) || 'User';

  return (
    <Box sx={{ mb: 1.5, position: 'relative' }}>
      <Box
        sx={{
          position: 'absolute', left: 0, top: 8,
          width: 28, height: 28, borderRadius: '50%',
          bgcolor: 'primary.main', display: 'flex',
          alignItems: 'center', justifyContent: 'center', zIndex: 1,
        }}
      >
        <AccountCircle sx={{ fontSize: 28, color: 'white' }} />
      </Box>

      <Box
        sx={(theme) => ({
          ml: 4, my: 1, mr: 1, p: 1.5, borderRadius: 1.5,
          bgcolor: 'grey.50',
          border: '1px solid',
          borderColor: alpha(theme.palette.grey[300], 0.4),
        })}
      >
        <Typography
          variant="caption"
          sx={{
            fontWeight: 600, fontSize: '0.7rem', color: 'primary.main',
            mb: 0.75, display: 'block', textTransform: 'uppercase', letterSpacing: 0.3,
          }}
        >
          {author}
        </Typography>
        <Typography
          variant="body1"
          sx={{
            whiteSpace: 'pre-wrap', wordBreak: 'break-word',
            lineHeight: 1.6, fontSize: '0.95rem', color: 'text.primary',
          }}
        >
          {item.content}
        </Typography>
      </Box>
    </Box>
  );
}

export default memo(UserQuestionItem);
