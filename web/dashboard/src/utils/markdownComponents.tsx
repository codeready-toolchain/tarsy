/**
 * Shared markdown rendering utilities
 * Used by timeline items and FinalAnalysisCard for consistent markdown rendering
 */

import type { HTMLAttributes, ReactNode } from 'react';
import { Box, Typography } from '@mui/material';
import { alpha } from '@mui/material/styles';
import type { Theme } from '@mui/material/styles';
import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter';
import { vs } from 'react-syntax-highlighter/dist/esm/styles/prism';
import CopyButton from '../components/shared/CopyButton';

/**
 * Type for react-markdown component override props.
 * react-markdown passes standard HTML attributes plus a `node` property from
 * the hast AST. The `inline` property is passed to the `code` component.
 */
type MdProps = HTMLAttributes<HTMLElement> & {
  node?: unknown;
  inline?: boolean;
  children?: ReactNode;
};

/**
 * Helper function to detect if text contains markdown syntax
 * Used for hybrid rendering approach - only parse markdown when needed
 */
export const hasMarkdownSyntax = (text: string): boolean => {
  // Check for common markdown patterns: bold, italic, code, lists, links
  return /[*_`[\]#-]/.test(text);
};

/**
 * Shared CSS-based markdown styles for executive summaries and hover cards
 * Used by FinalAnalysisCard and AlertListItem for consistent lightweight rendering
 */
export const executiveSummaryMarkdownStyles = (theme: Theme) => ({
  // Ensure markdown content renders inline properly
  '& p': {
    margin: 0,
    marginBottom: 1,
    lineHeight: 1.7,
    fontSize: '0.95rem',
    color: 'text.primary',
    '&:last-child': { marginBottom: 0 },
  },
  '& strong': {
    fontWeight: 'bold',
  },
  '& em': {
    fontStyle: 'italic',
  },
  // Inline code styling - using native CSS for proper inline behavior
  '& code': {
    fontFamily: '"JetBrains Mono", "Fira Code", "SF Mono", Consolas, monospace',
    fontSize: '0.875em',
    backgroundColor: alpha(theme.palette.grey[900], 0.08),
    color: 'error.main',
    padding: '1px 6px',
    borderRadius: '4px',
    border: '1px solid',
    borderColor: alpha(theme.palette.grey[900], 0.12),
    whiteSpace: 'nowrap',
    verticalAlign: 'baseline',
  },
  // Block code
  '& pre': {
    display: 'block',
    fontFamily: '"JetBrains Mono", "Fira Code", "SF Mono", Consolas, monospace',
    fontSize: '0.875em',
    backgroundColor: alpha(theme.palette.grey[900], 0.06),
    padding: 1.5,
    borderRadius: 1,
    overflowX: 'auto',
    margin: '8px 0',
    '& code': {
      backgroundColor: 'transparent',
      border: 'none',
      padding: 0,
      whiteSpace: 'pre',
    },
  },
  // Lists
  '& ul, & ol': {
    paddingLeft: 2.5,
    margin: '8px 0',
  },
  '& li': {
    marginBottom: 0.5,
    lineHeight: 1.6,
  },
});

/**
 * Memoized markdown components for final answer rendering.
 * Matches the old FinalAnalysisCard inline component styles exactly.
 */
export const finalAnswerMarkdownComponents = {
  h1: (props: MdProps) => {
    const { node: _node, children, ...safeProps } = props;
    return (
      <Typography
        variant="h5"
        sx={{ fontWeight: 'bold', color: 'primary.main' }}
        gutterBottom
        {...safeProps}
      >
        {children}
      </Typography>
    );
  },
  h2: (props: MdProps) => {
    const { node: _node, children, ...safeProps } = props;
    return (
      <Typography
        variant="h6"
        sx={{ fontWeight: 'bold', color: 'primary.main', mt: 2 }}
        gutterBottom
        {...safeProps}
      >
        {children}
      </Typography>
    );
  },
  h3: (props: MdProps) => {
    const { node: _node, children, ...safeProps } = props;
    return (
      <Typography
        variant="subtitle1"
        sx={{ fontWeight: 'bold', color: 'primary.main', mt: 1.5 }}
        gutterBottom
        {...safeProps}
      >
        {children}
      </Typography>
    );
  },
  p: (props: MdProps) => {
    const { node: _node, children, ...safeProps } = props;
    return (
      <Typography
        variant="body1"
        sx={{ mb: 1, lineHeight: 1.6, fontSize: '0.95rem' }}
        {...safeProps}
      >
        {children}
      </Typography>
    );
  },
  ul: (props: MdProps) => {
    const { node: _node, children, ...safeProps } = props;
    return (
      <Box component="ul" sx={{ mb: 1, pl: 2 }} {...safeProps}>
        {children}
      </Box>
    );
  },
  ol: (props: MdProps) => {
    const { node: _node, children, ...safeProps } = props;
    return (
      <Box component="ol" sx={{ mb: 1, pl: 2 }} {...safeProps}>
        {children}
      </Box>
    );
  },
  li: (props: MdProps) => {
    const { node: _node, children, ...safeProps } = props;
    return (
      <Typography
        component="li"
        variant="body1"
        sx={{ mb: 0.5, lineHeight: 1.6, fontSize: '0.95rem' }}
        {...safeProps}
      >
        {children}
      </Typography>
    );
  },
  // Block code wrapper: uses a plain <div> so that SyntaxHighlighter (which
  // renders its own <pre>) doesn't cause nested <pre> elements. For plain
  // code blocks without a language, the inner <code> element renders the
  // block styling. The `& code` selector resets inline code styles.
  pre: (props: MdProps) => {
    const { node: _node, children, ...safeProps } = props;
    return (
      <Box
        component="div"
        sx={{
          my: 1,
          '& > code': {
            // Plain block code without language — style as pre block
            display: 'block',
            backgroundColor: 'rgba(0, 0, 0, 0.06)',
            padding: '12px',
            borderRadius: '4px',
            overflowX: 'auto',
            fontFamily: 'monospace',
            fontSize: '0.85rem',
            whiteSpace: 'pre-wrap',
            wordBreak: 'break-word',
            // Reset inline code styles
            color: 'inherit',
            border: 'none',
          },
        }}
        {...safeProps}
      >
        {children}
      </Box>
    );
  },
  // Code element: handles both inline code and fenced code with language.
  // When inside a <pre> wrapper (block code without language), the parent's
  // `& > code` selector applies block styling. This avoids the <pre>-inside-<p>
  // nesting issue that caused hydration errors.
  code: (props: MdProps) => {
    const { node: _node, inline: _inline, className, children, ...safeProps } = props;

    // Fenced code block with language — render with syntax highlighting
    const match = /language-(\w+)/.exec(className || '');
    if (match) {
      const language = match[1];
      const codeString = String(children).replace(/\n$/, '');
      return (
        <Box sx={{ position: 'relative', my: 1 }}>
          <Box
            sx={{
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'center',
              bgcolor: 'grey.200',
              px: 1.5,
              py: 0.5,
              borderRadius: '4px 4px 0 0',
              borderBottom: '1px solid',
              borderColor: 'divider',
            }}
          >
            <Typography variant="caption" sx={{ fontWeight: 600, textTransform: 'uppercase' }}>
              {language}
            </Typography>
            <CopyButton text={codeString} variant="icon" size="small" tooltip="Copy code" />
          </Box>
          <SyntaxHighlighter
            language={language}
            style={vs}
            customStyle={{
              margin: 0,
              padding: '12px',
              fontSize: '0.875rem',
              lineHeight: 1.5,
              borderRadius: '0 0 4px 4px',
            }}
            wrapLines
            wrapLongLines
          >
            {codeString}
          </SyntaxHighlighter>
        </Box>
      );
    }

    // Inline code (or block code without language — styled by parent pre's CSS)
    return (
      <Box
        component="code"
        sx={{
          backgroundColor: 'rgba(0, 0, 0, 0.08)',
          color: 'error.main',
          padding: '2px 6px',
          border: '1px solid',
          borderColor: 'rgba(0, 0, 0, 0.1)',
          borderRadius: '4px',
          fontFamily: 'monospace',
          fontSize: '0.85rem',
        }}
        {...safeProps}
      >
        {children}
      </Box>
    );
  },
  strong: (props: MdProps) => {
    const { node: _node, children, ...safeProps } = props;
    return (
      <Box component="strong" sx={{ fontWeight: 700 }} {...safeProps}>
        {children}
      </Box>
    );
  },
  blockquote: (props: MdProps) => {
    const { node: _node, children, ...safeProps } = props;
    return (
      <Box
        component="blockquote"
        sx={{
          borderLeft: '4px solid',
          borderColor: 'primary.main',
          pl: 2,
          ml: 0,
          my: 1,
          color: 'text.secondary',
          fontStyle: 'italic',
        }}
        {...safeProps}
      >
        {children}
      </Box>
    );
  },
};

/**
 * Lightweight markdown components for thoughts and summarizations
 * Similar to finalAnswerMarkdownComponents but simpler styling
 */
export const thoughtMarkdownComponents = {
  p: (props: MdProps) => {
    const { node: _node, children, ...safeProps } = props;
    return (
      <Typography variant="body1" sx={{ mb: 0.5, lineHeight: 1.7, fontSize: '1rem' }} {...safeProps}>
        {children}
      </Typography>
    );
  },
  strong: (props: MdProps) => {
    const { node: _node, children, ...safeProps } = props;
    return (
      <Box component="strong" sx={{ fontWeight: 700 }} {...safeProps}>
        {children}
      </Box>
    );
  },
  em: (props: MdProps) => {
    const { node: _node, children, ...safeProps } = props;
    return (
      <Box component="em" sx={{ fontStyle: 'italic' }} {...safeProps}>
        {children}
      </Box>
    );
  },
  // Block code wrapper: renders fenced code blocks. The inner <code> element
  // is rendered by the `code` component below which always uses inline styling.
  // The `& code` selector resets those inline styles inside the pre context.
  pre: (props: MdProps) => {
    const { node: _node, children, ...safeProps } = props;
    return (
      <Box
        component="pre"
        sx={{
          bgcolor: 'grey.100',
          padding: '12px',
          borderRadius: 1,
          overflowX: 'auto',
          fontFamily: 'monospace',
          fontSize: '0.9em',
          margin: '8px 0',
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-word',
          '& code': {
            backgroundColor: 'transparent',
            padding: 0,
            border: 'none',
            borderRadius: 0,
            fontSize: 'inherit',
          },
        }}
        {...safeProps}
      >
        {children}
      </Box>
    );
  },
  // Code element: always renders as inline <code>. When inside a <pre> (block
  // code), the parent pre's `& code` selector resets the inline styles. This
  // avoids the <pre>-inside-<p> nesting issue that occurs when react-markdown
  // doesn't pass the `inline` prop reliably.
  code: (props: MdProps) => {
    const { node: _node, inline: _inline, className: _className, children, ...safeProps } = props;
    return (
      <Box
        component="code"
        sx={{
          bgcolor: 'grey.100',
          px: 0.5,
          py: 0.25,
          borderRadius: 0.5,
          fontFamily: 'monospace',
          fontSize: '0.9em',
        }}
        {...safeProps}
      >
        {children}
      </Box>
    );
  },
  ul: (props: MdProps) => {
    const { node: _node, children, ...safeProps } = props;
    return (
      <Box component="ul" sx={{ mb: 0.5, pl: 2.5 }} {...safeProps}>
        {children}
      </Box>
    );
  },
  ol: (props: MdProps) => {
    const { node: _node, children, ...safeProps } = props;
    return (
      <Box component="ol" sx={{ mb: 0.5, pl: 2.5 }} {...safeProps}>
        {children}
      </Box>
    );
  },
  li: (props: MdProps) => {
    const { node: _node, children, ...safeProps } = props;
    return (
      <Typography
        component="li"
        variant="body1"
        sx={{ mb: 0.3, lineHeight: 1.6, fontSize: '1rem' }}
        {...safeProps}
      >
        {children}
      </Typography>
    );
  },
};
