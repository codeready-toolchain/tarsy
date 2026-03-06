import { memo, useState } from 'react';
import { Box, Collapse, IconButton, Typography, alpha } from '@mui/material';
import { ErrorOutline, ExpandMore, ExpandLess } from '@mui/icons-material';
import { highlightSearchTermNodes } from '../../utils/search';

interface ErrorCardProps {
  label: string;
  message?: string;
  /** Outer margin — pass e.g. `{ mt: 2 }` to control spacing in context. */
  sx?: Record<string, unknown>;
  searchTerm?: string;
}

function formatErrorMessage(raw: string): string {
  return raw
    .replace(/\\n/g, '\n')
    .replace(/\)\s*\(/g, ')\n(')
    .trim();
}

function stripEnvelope(raw: string): string {
  let msg = raw.replace(/^LLM error:\s*/i, '');
  msg = msg.replace(/\s*\(code:\s*\w+,?\s*retryable:\s*\w+\)\s*$/i, '');
  msg = msg.replace(/\s*\(attempt\s*\d+\)\s*$/i, '');
  return msg.trim();
}

const PREVIEW_LENGTH = 120;

function ErrorCard({ label, message, sx: outerSx, searchTerm }: ErrorCardProps) {
  const stripped = stripEnvelope((message || '').trim());
  const formatted = formatErrorMessage(stripped);
  const hasContent = formatted.length > 0;

  const [expanded, setExpanded] = useState(false);

  const preview = stripped.length > PREVIEW_LENGTH
    ? stripped.slice(0, PREVIEW_LENGTH) + '…'
    : stripped;

  return (
    <Box sx={outerSx}>
      <Box sx={{ display: 'flex', alignItems: 'stretch' }}>
        <Box
          sx={(theme) => ({
            width: 4,
            bgcolor: theme.palette.error.main,
            borderRadius: 1,
            flexShrink: 0,
          })}
        />
        <Box sx={{ flex: 1, minWidth: 0 }}>
          <Box
            onClick={() => hasContent && setExpanded((prev) => !prev)}
            sx={(theme) => ({
              display: 'flex',
              alignItems: 'center',
              gap: 1,
              flexWrap: 'wrap',
              px: 1.5,
              py: 0.75,
              bgcolor: alpha(theme.palette.error.main, 0.06),
              borderTopRightRadius: 4,
              borderBottomRightRadius: expanded ? 0 : 4,
              cursor: hasContent ? 'pointer' : 'default',
              transition: 'background-color 0.2s ease',
              '&:hover': hasContent ? { bgcolor: alpha(theme.palette.error.main, 0.12) } : {},
            })}
          >
            <ErrorOutline sx={{ fontSize: 18, color: 'error.main' }} />
            <Typography
              variant="caption"
              sx={{
                fontWeight: 800,
                color: 'error.main',
                fontSize: '0.75rem',
                letterSpacing: 0.3,
                textTransform: 'uppercase',
              }}
            >
              {label}
            </Typography>

            {!expanded && preview && (
              <Typography
                variant="caption"
                sx={{
                  fontFamily: 'monospace',
                  fontSize: '0.72rem',
                  color: 'text.secondary',
                  whiteSpace: 'nowrap',
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  minWidth: 0,
                  flex: 1,
                }}
              >
                {preview}
              </Typography>
            )}

            {hasContent && (
              <IconButton size="small" sx={{ p: 0.25, ml: 'auto' }}>
                {expanded ? <ExpandLess fontSize="small" /> : <ExpandMore fontSize="small" />}
              </IconButton>
            )}
          </Box>

          <Collapse in={expanded}>
            <Box
              sx={(theme) => ({
                px: 1.5,
                py: 1,
                bgcolor: alpha(theme.palette.error.main, 0.04),
                borderTop: '1px solid',
                borderColor: alpha(theme.palette.error.main, 0.15),
                borderBottomRightRadius: 4,
              })}
            >
              <Box
                component="pre"
                sx={(theme) => ({
                  m: 0,
                  px: 1.5,
                  py: 1,
                  fontFamily: 'monospace',
                  fontSize: '0.72rem',
                  lineHeight: 1.6,
                  whiteSpace: 'pre-wrap',
                  wordBreak: 'break-word',
                  color: theme.palette.text.secondary,
                  bgcolor: theme.palette.grey[50],
                  border: `1px solid ${theme.palette.divider}`,
                  borderRadius: 1,
                  maxHeight: 250,
                  overflow: 'auto',
                })}
              >
                {searchTerm ? highlightSearchTermNodes(formatted, searchTerm) : formatted}
              </Box>
            </Box>
          </Collapse>
        </Box>
      </Box>
    </Box>
  );
}

export default memo(ErrorCard);
