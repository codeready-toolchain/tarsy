import { useState, useEffect } from 'react';
import {
  Paper,
  Typography,
  Box,
  Chip,
  IconButton,
  Collapse,
  Skeleton,
  Alert,
  Button,
} from '@mui/material';
import { ExpandMore, School } from '@mui/icons-material';
import { alpha } from '@mui/material/styles';

import { getSessionMemories } from '../../services/api.ts';
import type { MemoryItem } from '../../types/session.ts';

interface ExtractedLearningsCardProps {
  sessionId: string;
  /** Whether the session has been scored (only show card when scored). */
  hasScore: boolean;
}

const valenceColor: Record<string, 'success' | 'error' | 'default'> = {
  positive: 'success',
  negative: 'error',
  neutral: 'default',
};

const categoryLabel: Record<string, string> = {
  semantic: 'Fact',
  episodic: 'Experience',
  procedural: 'Strategy',
};

export default function ExtractedLearningsCard({ sessionId, hasScore }: ExtractedLearningsCardProps) {
  const [memories, setMemories] = useState<MemoryItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [fetchError, setFetchError] = useState<Error | null>(null);
  const [isExpanded, setIsExpanded] = useState(true);
  const [retryCount, setRetryCount] = useState(0);

  useEffect(() => {
    if (!hasScore) {
      setLoading(false);
      return;
    }

    let cancelled = false;
    setLoading(true);
    setFetchError(null);
    getSessionMemories(sessionId)
      .then((data) => {
        if (!cancelled) setMemories(data);
      })
      .catch((err) => {
        if (!cancelled) setFetchError(err instanceof Error ? err : new Error(String(err)));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => { cancelled = true; };
  }, [sessionId, hasScore, retryCount]);

  if (!hasScore) {
    return null;
  }

  if (loading) {
    return <Skeleton variant="rectangular" height={60} sx={{ borderRadius: 1 }} />;
  }

  if (fetchError) {
    return (
      <Alert
        severity="warning"
        action={
          <Button color="inherit" size="small" onClick={() => setRetryCount((c) => c + 1)}>
            Retry
          </Button>
        }
      >
        Failed to load lessons for this session.
      </Alert>
    );
  }

  return (
    <Paper sx={{ p: 3 }}>
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          mb: isExpanded ? 2 : 0,
        }}
      >
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          <School fontSize="small" sx={{ color: 'info.main' }} />
          <Typography variant="h6" sx={{ fontWeight: 600 }}>
            Lessons Learned
          </Typography>
          {memories.length > 0 && (
            <Chip
              label={memories.length}
              size="small"
              variant="outlined"
              sx={{ ml: 0.5, height: 20, fontSize: '0.75rem' }}
            />
          )}
        </Box>
        <IconButton
          size="small"
          onClick={() => setIsExpanded(!isExpanded)}
          aria-label={isExpanded ? 'Collapse lessons' : 'Expand lessons'}
          sx={{
            transition: 'transform 0.4s',
            transform: isExpanded ? 'rotate(180deg)' : 'rotate(0deg)',
          }}
        >
          <ExpandMore />
        </IconButton>
      </Box>

      <Collapse in={isExpanded} timeout={400}>
        {memories.length === 0 ? (
          <Typography variant="body2" color="text.secondary" sx={{ fontStyle: 'italic' }}>
            No new lessons were captured from this investigation.
          </Typography>
        ) : (
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
            {memories.map((mem) => (
              <Box
                key={mem.id}
                sx={(theme) => ({
                  display: 'flex',
                  gap: 1.5,
                  alignItems: 'flex-start',
                  p: 1.5,
                  borderRadius: 1,
                  bgcolor: alpha(theme.palette.info.main, 0.04),
                  border: '1px solid',
                  borderColor: alpha(theme.palette.info.main, 0.12),
                })}
              >
                <Chip
                  label={categoryLabel[mem.category] ?? mem.category}
                  size="small"
                  variant="outlined"
                  color="info"
                  sx={{ flexShrink: 0, height: 24, fontSize: '0.7rem' }}
                />
                <Box sx={{ flex: 1, minWidth: 0 }}>
                  <Typography
                    variant="body2"
                    sx={{ lineHeight: 1.6, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}
                  >
                    {mem.content}
                  </Typography>
                </Box>
                <Chip
                  label={mem.valence}
                  size="small"
                  color={valenceColor[mem.valence] ?? 'default'}
                  variant="outlined"
                  sx={{ flexShrink: 0, height: 22, fontSize: '0.7rem' }}
                />
              </Box>
            ))}
          </Box>
        )}
      </Collapse>
    </Paper>
  );
}
