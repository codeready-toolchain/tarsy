import { memo, useMemo } from 'react';
import { Box, Typography, alpha } from '@mui/material';
import { AccountCircle, Assignment } from '@mui/icons-material';
import ReactMarkdown from 'react-markdown';
import { remarkPlugins, thoughtMarkdownComponents } from '../../utils/markdownComponents';
import { rehypeSearchHighlight } from '../../utils/rehypeSearchHighlight';
import type { FlowItem } from '../../utils/timelineParser';

interface UserQuestionItemProps {
  item: FlowItem;
  searchTerm?: string;
}

const MAX_TASK_HEIGHT = 200;

function UserQuestionItem({ item, searchTerm }: UserQuestionItemProps) {
  const author = (item.metadata?.author as string) || 'User';
  const isTask = author === 'Task';
  const Icon = isTask ? Assignment : AccountCircle;
  const accentColor = isTask ? 'secondary.main' : 'primary.main';
  const rehypePlugins = useMemo(
    () => { const p = rehypeSearchHighlight(searchTerm || ''); return p ? [p] : []; },
    [searchTerm],
  );

  return (
    <Box data-flow-item-id={item.id} sx={{ mb: 1.5, position: 'relative' }}>
      <Box
        sx={{
          position: 'absolute', left: 0, top: 8,
          width: 28, height: 28, borderRadius: '50%',
          bgcolor: accentColor, display: 'flex',
          alignItems: 'center', justifyContent: 'center', zIndex: 1,
        }}
      >
        <Icon sx={{ fontSize: isTask ? 18 : 28, color: 'white' }} />
      </Box>

      <Box
        sx={(theme) => ({
          ml: 4, my: 1, mr: 1, p: 1.5, borderRadius: 1.5,
          bgcolor: 'action.hover',
          border: '1px solid',
          borderColor: theme.palette.divider,
        })}
      >
        <Typography
          variant="caption"
          sx={{
            fontWeight: 600, fontSize: '0.7rem', color: accentColor,
            mb: 0.75, display: 'block', textTransform: 'uppercase', letterSpacing: 0.3,
          }}
        >
          {author}
        </Typography>
        <Box sx={{
          ...(isTask && { maxHeight: MAX_TASK_HEIGHT, overflowY: 'auto' }),
          fontSize: '0.95rem', lineHeight: 1.6, color: 'text.primary',
          '& p:first-of-type': { mt: 0 },
          '& p:last-of-type': { mb: 0 },
        }}>
          <ReactMarkdown
            remarkPlugins={remarkPlugins}
            rehypePlugins={rehypePlugins}
            components={thoughtMarkdownComponents}
          >
            {item.content}
          </ReactMarkdown>
        </Box>
      </Box>
    </Box>
  );
}

export default memo(UserQuestionItem);
