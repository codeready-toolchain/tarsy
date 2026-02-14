/**
 * ContentPreviewTooltip Component
 * Displays full content in a tooltip when hovering over collapsed items
 * Supports markdown rendering for different content types
 */

import { Tooltip, Paper } from '@mui/material';
import ReactMarkdown from 'react-markdown';
import { 
  thoughtMarkdownComponents, 
  finalAnswerMarkdownComponents 
} from '../../utils/markdownComponents';

interface ContentPreviewTooltipProps {
  /** Full content to display in tooltip */
  content: string;
  /** Type of content (determines markdown rendering style) */
  type: 'thought' | 'final_answer' | 'forced_conclusion' | 'summarization' | 'native_thinking';
  /** Child element that triggers the tooltip */
  children: React.ReactElement;
}

/**
 * Wraps collapsed content with hover tooltip showing full content
 * Features:
 * - 300ms enter delay to avoid accidental triggers
 * - Max width 800px, max height 600px with scroll
 * - Renders full markdown using appropriate components
 * - Positioned top-start to avoid cursor interference
 */
export default function ContentPreviewTooltip({ 
  content, 
  type, 
  children 
}: ContentPreviewTooltipProps) {
  // Select appropriate markdown components based on content type
  const markdownComponents = (type === 'final_answer' || type === 'forced_conclusion')
    ? finalAnswerMarkdownComponents 
    : thoughtMarkdownComponents;
  
  return (
    <Tooltip
      title={
        <Paper 
          elevation={8} 
          sx={{ 
            p: 3, 
            maxWidth: 800, 
            maxHeight: 600, 
            overflow: 'auto',
            bgcolor: 'grey.100',
            color: 'grey.900',
            border: '2px solid',
            borderColor: 'primary.main',
            '& p, & li, & span': {
              color: 'grey.900'
            },
            '& code': {
              bgcolor: 'grey.200',
              color: 'primary.dark'
            }
          }}
        >
          <ReactMarkdown components={markdownComponents}>
            {content}
          </ReactMarkdown>
        </Paper>
      }
      enterDelay={300}
      placement="top-start"
      slotProps={{
        popper: {
          sx: {
            '& .MuiTooltip-tooltip': {
              bgcolor: 'transparent',
              maxWidth: 'none',
              p: 0
            }
          }
        }
      }}
    >
      {children}
    </Tooltip>
  );
}
