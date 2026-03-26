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
import { ExpandMore, Lightbulb } from '@mui/icons-material';
import { alpha } from '@mui/material/styles';

import { getInjectedMemories } from '../../services/api.ts';
import type { MemoryItem } from '../../types/session.ts';

interface InjectedMemoriesCardProps {
  sessionId: string;
}

const valenceColor: Record<string, 'success' | 'error' | 'default'> = {
  positive: 'success',
  negative: 'error',
  neutral: 'default',
};

const categoryIcon: Record<string, string> = {
  semantic: 'S',
  episodic: 'E',
  procedural: 'P',
};

export default function InjectedMemoriesCard({ sessionId }: InjectedMemoriesCardProps) {
  const [memories, setMemories] = useState<MemoryItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [fetchError, setFetchError] = useState<Error | null>(null);
  const [isExpanded, setIsExpanded] = useState(true);
  const [retryCount, setRetryCount] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setFetchError(null);
    getInjectedMemories(sessionId)
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
  }, [sessionId, retryCount]);

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
        Failed to load injected memories.
      </Alert>
    );
  }

  if (memories.length === 0) {
    return null;
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
          <Lightbulb fontSize="small" sx={{ color: 'warning.main' }} />
          <Typography variant="h6" sx={{ fontWeight: 600 }}>
            Lessons from Past Investigations
          </Typography>
          <Chip
            label={memories.length}
            size="small"
            variant="outlined"
            sx={{ ml: 0.5, height: 20, fontSize: '0.75rem' }}
          />
        </Box>
        <IconButton
          size="small"
          onClick={() => setIsExpanded(!isExpanded)}
          aria-label={isExpanded ? 'Collapse memories' : 'Expand memories'}
          sx={{
            transition: 'transform 0.4s',
            transform: isExpanded ? 'rotate(180deg)' : 'rotate(0deg)',
          }}
        >
          <ExpandMore />
        </IconButton>
      </Box>

      <Collapse in={isExpanded} timeout={400}>
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
                bgcolor: alpha(theme.palette.warning.main, 0.04),
                border: '1px solid',
                borderColor: alpha(theme.palette.warning.main, 0.12),
              })}
            >
              <Chip
                label={categoryIcon[mem.category] ?? '?'}
                size="small"
                variant="outlined"
                sx={{ minWidth: 28, height: 24, fontSize: '0.7rem', fontWeight: 700 }}
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
      </Collapse>
    </Paper>
  );
}
