import { useState, useEffect, useMemo, memo } from 'react';
import { Box, Typography, Collapse, IconButton, alpha } from '@mui/material';
import { ExpandMore, ExpandLess, AutoStoriesOutlined } from '@mui/icons-material';
import ReactMarkdown from 'react-markdown';
import CopyButton from '../shared/CopyButton';
import { remarkPlugins, thoughtMarkdownComponents } from '../../utils/markdownComponents';
import { rehypeSearchHighlight } from '../../utils/rehypeSearchHighlight';
import type { FlowItem } from '../../utils/timelineParser';

interface SkillLoadedItemProps {
  item: FlowItem;
  expandAll?: boolean;
  searchTerm?: string;
}

function SkillLoadedItem({ item, expandAll = false, searchTerm }: SkillLoadedItemProps) {
  const [expanded, setExpanded] = useState(false);
  useEffect(() => {
    setExpanded(expandAll);
  }, [expandAll]);
  const isExpanded = expandAll || expanded;

  const rehypePlugins = useMemo(
    () => { const p = rehypeSearchHighlight(searchTerm || ''); return p ? [p] : []; },
    [searchTerm],
  );
  const skillName = (item.metadata?.skill_name as string) || 'Skill';

  return (
    <Box
      data-flow-item-id={item.id}
      sx={(theme) => ({
        ml: 4, my: 0.5, mr: 1,
        border: '1px solid',
        borderColor: alpha(theme.palette.info.main, 0.25),
        borderRadius: 1.5,
        bgcolor: alpha(theme.palette.info.main, 0.04),
      })}
    >
      <Box
        sx={(theme) => ({
          display: 'flex', alignItems: 'center', gap: 1, px: 1.5, py: 0.75,
          cursor: 'pointer', borderRadius: 1.5, transition: 'background-color 0.2s ease',
          '&:hover': { bgcolor: alpha(theme.palette.info.main, 0.1) },
        })}
        onClick={() => {
          if (expandAll) return;
          setExpanded((prev) => !prev);
        }}
      >
        <AutoStoriesOutlined sx={(theme) => ({ fontSize: 18, color: theme.palette.info.main })} />
        <Typography variant="body2" sx={{ fontFamily: 'monospace', fontWeight: 500, fontSize: '0.9rem', color: 'text.secondary' }}>
          Pre-loaded Skill
        </Typography>
        <Typography variant="caption" color="text.secondary" sx={{ fontSize: '0.8rem', flex: 1, lineHeight: 1.4 }}>
          {skillName}
        </Typography>
        <IconButton size="small" sx={{ p: 0.25 }}>
          {isExpanded ? <ExpandLess fontSize="small" /> : <ExpandMore fontSize="small" />}
        </IconButton>
      </Box>

      <Collapse in={isExpanded}>
        <Box sx={{ px: 1.5, pb: 1.5, pt: 0.5, borderTop: 1, borderColor: 'divider' }}>
          <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 0.5 }}>
            <Typography variant="caption" color="text.secondary">
              Injected into system prompt at investigation start
            </Typography>
            <CopyButton text={item.content || ''} variant="icon" size="small" tooltip="Copy skill content" />
          </Box>
          {item.content ? (
            <Box sx={(theme) => ({
              maxHeight: 400, overflow: 'auto',
              p: 1.5, borderRadius: 1,
              bgcolor: '#fff',
              border: `1px solid ${theme.palette.divider}`,
              fontSize: '0.85rem',
              ...theme.applyStyles('dark', {
                bgcolor: 'rgba(255, 255, 255, 0.06)',
              }),
              '& h1': { fontSize: '1.1rem', mt: 0, mb: 1 },
              '& h2': { fontSize: '1rem', mt: 1.5, mb: 0.75 },
              '& h3': { fontSize: '0.9rem', mt: 1, mb: 0.5 },
              '& p': { my: 0.5, lineHeight: 1.6 },
              '& ul, & ol': { pl: 2.5, my: 0.5 },
              '& li': { my: 0.25 },
              '& code': {
                fontFamily: 'monospace', fontSize: '0.8rem',
                bgcolor: alpha(theme.palette.info.main, 0.08),
                px: 0.5, py: 0.25, borderRadius: 0.5,
              },
              '& pre': { my: 1, p: 1.5, borderRadius: 1, bgcolor: theme.palette.action.selected, overflow: 'auto' },
              '& pre code': { bgcolor: 'transparent', px: 0, py: 0 },
              '& table': { borderCollapse: 'collapse', width: '100%', my: 1, fontSize: '0.8rem' },
              '& th, & td': { border: `1px solid ${theme.palette.divider}`, px: 1, py: 0.5, textAlign: 'left' },
              '& th': { bgcolor: theme.palette.action.selected, fontWeight: 600 },
              '& hr': { my: 1.5, borderColor: theme.palette.divider },
              '& strong': { fontWeight: 600 },
            })}>
              <ReactMarkdown
                components={thoughtMarkdownComponents}
                remarkPlugins={remarkPlugins}
                rehypePlugins={rehypePlugins}
                skipHtml
              >
                {item.content}
              </ReactMarkdown>
            </Box>
          ) : (
            <Typography variant="caption" color="text.secondary" sx={{ fontStyle: 'italic' }}>No content</Typography>
          )}
        </Box>
      </Collapse>
    </Box>
  );
}

export default memo(SkillLoadedItem);
