import { useState, useEffect, forwardRef } from 'react';
import { Paper, Typography, Box, Button, Alert, AlertTitle, Snackbar, Collapse, IconButton } from '@mui/material';
import { Psychology, ContentCopy, ExpandMore, AutoAwesome } from '@mui/icons-material';
import { alpha } from '@mui/material/styles';
import ReactMarkdown, { defaultUrlTransform } from 'react-markdown';
import CopyButton from '../shared/CopyButton';
import { isTerminalStatus, SESSION_STATUS, type SessionStatus } from '../../constants/sessionStatus';
import { executiveSummaryMarkdownStyles } from '../../utils/markdownComponents';
import { finalAnswerMarkdownComponents } from '../../utils/markdownComponents';

/** Copy text to clipboard, using the modern Clipboard API with no legacy fallback. */
function copyToClipboard(text: string, onSuccess: () => void) {
  if (navigator.clipboard?.writeText) {
    navigator.clipboard.writeText(text).then(onSuccess).catch(() => { /* ignore */ });
  }
}

interface FinalAnalysisCardProps {
  analysis: string | null;
  summary: string | null;
  sessionStatus: string;
  errorMessage: string | null;
  /** Increment to collapse the card externally (e.g. Jump to Chat) */
  collapseCounter?: number;
  /** Increment to expand the card externally (e.g. Jump to Summary) */
  expandCounter?: number;
}

/**
 * Generate a placeholder analysis for terminal sessions with no real analysis.
 */
function generateFakeAnalysis(status: string, errorMessage?: string | null): string {
  switch (status) {
    case SESSION_STATUS.CANCELLED:
      return '# Session Cancelled\n\n**Status:** Session was terminated before the AI could complete its analysis.\n\nThis analysis session was cancelled before completion. No final analysis is available.\n\nIf you need to investigate this alert, please submit a new analysis session.';
    case SESSION_STATUS.FAILED:
      return `# Session Failed\n\nThis analysis session failed before completion.\n\n**Error Details:**\n${errorMessage ? `\`\`\`\n${errorMessage}\n\`\`\`` : '_No error details available_'}\n\nPlease review the session logs or submit a new analysis session.`;
    case SESSION_STATUS.COMPLETED:
      return '# Analysis Completed\n\n**Note:** This may indicate the session completed but no structured final analysis was generated.\n\nThis session completed successfully, but no final analysis was generated. Please check the session stages for details.';
    default:
      return `# No Analysis Available\n\nThis session has reached a terminal state (${status}), but no final analysis is available.\n\nPlease review the reasoning flow above for any partial findings.`;
  }
}

/**
 * FinalAnalysisCard - renders the AI analysis with executive summary,
 * expand/collapse, copy-to-clipboard, and AI warning.
 * Supports counter-based expand/collapse from parent.
 */
const FinalAnalysisCard = forwardRef<HTMLDivElement, FinalAnalysisCardProps>(
  ({ analysis, summary, sessionStatus, errorMessage, collapseCounter = 0, expandCounter = 0 }, ref) => {
    const [analysisExpanded, setAnalysisExpanded] = useState(false);
    const [copySuccess, setCopySuccess] = useState(false);
    const [prevAnalysis, setPrevAnalysis] = useState<string | null>(null);
    const [isNewlyUpdated, setIsNewlyUpdated] = useState(false);

    // Counter-driven collapse
    useEffect(() => {
      if (collapseCounter > 0) setAnalysisExpanded(false);
    }, [collapseCounter]);

    // Counter-driven expand
    useEffect(() => {
      if (expandCounter > 0) setAnalysisExpanded(true);
    }, [expandCounter]);

    // Auto-expand on first analysis and "Updated" indicator for active sessions
    useEffect(() => {
      if (analysis && analysis !== prevAnalysis) {
        const isActive = sessionStatus === SESSION_STATUS.IN_PROGRESS || sessionStatus === SESSION_STATUS.PENDING;
        const isFirst = !prevAnalysis && analysis;
        const isSignificant = prevAnalysis && analysis && Math.abs(analysis.length - prevAnalysis.length) > 100;

        if (isFirst) {
          setAnalysisExpanded(true);
          if (isActive) setIsNewlyUpdated(true);
        } else if (isSignificant && isActive) {
          setIsNewlyUpdated(true);
        }

        setPrevAnalysis(analysis);

        if ((isFirst || isSignificant) && isActive) {
          const timer = setTimeout(() => setIsNewlyUpdated(false), 3000);
          return () => clearTimeout(timer);
        }
      }
    }, [analysis, prevAnalysis, sessionStatus]);

    const displayAnalysis = analysis || (isTerminalStatus(sessionStatus as SessionStatus) ? generateFakeAnalysis(sessionStatus, errorMessage) : null);
    const isFakeAnalysis = !analysis && isTerminalStatus(sessionStatus as SessionStatus);

    const getCombinedDocument = () => {
      let doc = '';
      if (summary) doc += `# Executive Summary\n\n${summary}\n\n`;
      if (displayAnalysis) {
        if (summary) doc += '# Full Detailed Analysis\n\n';
        doc += displayAnalysis;
      }
      return doc;
    };

    if (!displayAnalysis) return null;

    return (
      <>
        <Paper ref={ref} sx={{ p: 3 }}>
          {/* Header */}
          <Box
            sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: analysisExpanded ? 2 : 0, cursor: 'pointer', '&:hover': { opacity: 0.8 } }}
            onClick={() => setAnalysisExpanded(!analysisExpanded)}
          >
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.5 }}>
              <Box sx={{ width: 40, height: 40, borderRadius: '50%', bgcolor: (theme) => alpha(theme.palette.primary.main, 0.15), border: '2px solid', borderColor: 'primary.main', display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 }}>
                <Psychology sx={{ fontSize: 24, color: 'primary.main' }} />
              </Box>
              <Typography variant="h6">Final AI Analysis</Typography>
              {isNewlyUpdated && (
                <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: 0.5, bgcolor: 'success.main', color: 'white', px: 1, py: 0.25, borderRadius: 1, fontSize: '0.75rem', fontWeight: 'medium', animation: 'pulse 2s ease-in-out infinite', '@keyframes pulse': { '0%': { opacity: 1 }, '50%': { opacity: 0.7 }, '100%': { opacity: 1 } } }}>
                  ✨ Updated
                </Box>
              )}
            </Box>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
              <Button
                startIcon={<ContentCopy />} variant="outlined" size="small"
                onClick={(e) => {
                  e.stopPropagation();
                  const text = getCombinedDocument();
                  copyToClipboard(text, () => setCopySuccess(true));
                }}
              >
                Copy {isFakeAnalysis ? 'Message' : 'Analysis'}
              </Button>
              <IconButton size="small" onClick={(e) => { e.stopPropagation(); setAnalysisExpanded(!analysisExpanded); }} sx={{ transition: 'transform 0.4s', transform: analysisExpanded ? 'rotate(180deg)' : 'rotate(0deg)' }}>
                <ExpandMore />
              </IconButton>
            </Box>
          </Box>

          {/* AI Warning */}
          {!isFakeAnalysis && (summary || displayAnalysis) && (
            <Alert severity="info" icon={<AutoAwesome />} sx={{ mt: 2, bgcolor: (theme) => alpha(theme.palette.info.main, 0.04), border: '1px solid', borderColor: (theme) => alpha(theme.palette.info.main, 0.2), '& .MuiAlert-icon': { color: 'info.main' } }}>
              <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 0.75 }}>
                <Typography variant="body2" sx={{ fontWeight: 600 }}>AI-Generated Content</Typography>
                <Typography variant="body2" color="text.secondary">Always review AI generated content prior to use.</Typography>
              </Box>
            </Alert>
          )}

          {/* Executive Summary — always visible */}
          {summary && (
            <Box sx={{ mt: 2 }}>
              <Box sx={{ bgcolor: (theme) => alpha(theme.palette.success.main, 0.10), border: '1px solid', borderColor: (theme) => alpha(theme.palette.success.main, 0.35), borderRadius: 2, p: 2.5, position: 'relative', overflow: 'hidden', '&::before': { content: '""', position: 'absolute', left: 0, top: 0, bottom: 0, width: 4, bgcolor: 'success.main', borderRadius: '4px 0 0 4px' } }}>
                <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: 1.5 }}>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                    <AutoAwesome sx={{ color: 'success.main', fontSize: 20 }} />
                    <Typography variant="subtitle2" sx={{ fontWeight: 700, color: 'success.main', textTransform: 'uppercase', letterSpacing: 0.5, fontSize: '0.8rem' }}>
                      Executive Summary
                    </Typography>
                  </Box>
                  <CopyButton text={summary} variant="icon" size="small" tooltip="Copy summary" />
                </Box>
                <Box sx={executiveSummaryMarkdownStyles}>
                  <ReactMarkdown>{summary}</ReactMarkdown>
                </Box>
              </Box>
            </Box>
          )}

          {/* Collapsible full analysis */}
          <Collapse in={analysisExpanded} timeout={400}>
            {summary && displayAnalysis && (
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 2, mt: 3, mb: 2, color: 'text.secondary' }}>
                <Box sx={{ flex: 1, height: '1px', bgcolor: 'divider' }} />
                <Typography variant="caption" sx={{ fontSize: '0.75rem', textTransform: 'uppercase', letterSpacing: 1, fontWeight: 600, color: 'text.disabled' }}>
                  Full Detailed Analysis
                </Typography>
                <Box sx={{ flex: 1, height: '1px', bgcolor: 'divider' }} />
              </Box>
            )}

            {isFakeAnalysis && (
              <Alert severity="warning" sx={{ mb: 2 }}>
                <Typography variant="body2">This session did not complete successfully.</Typography>
              </Alert>
            )}

            <Paper variant="outlined" sx={{ p: 3, bgcolor: 'grey.100' }}>
              <Box sx={{ display: 'flex', justifyContent: 'flex-end', mb: 2 }}>
                <CopyButton text={displayAnalysis} variant="icon" size="small" tooltip="Copy analysis" />
              </Box>
              <ReactMarkdown urlTransform={defaultUrlTransform} components={finalAnswerMarkdownComponents}>
                {displayAnalysis}
              </ReactMarkdown>
            </Paper>

            {sessionStatus === SESSION_STATUS.FAILED && errorMessage && !isFakeAnalysis && (
              <Alert severity="error" sx={{ mt: 2 }}>
                <AlertTitle>Session completed with errors</AlertTitle>
                <Typography variant="body2">{errorMessage}</Typography>
              </Alert>
            )}
          </Collapse>
        </Paper>

        <Snackbar
          open={copySuccess} autoHideDuration={3000}
          onClose={() => setCopySuccess(false)}
          message="Analysis copied to clipboard"
          anchorOrigin={{ vertical: 'bottom', horizontal: 'center' }}
        />
      </>
    );
  }
);

FinalAnalysisCard.displayName = 'FinalAnalysisCard';

export default FinalAnalysisCard;
