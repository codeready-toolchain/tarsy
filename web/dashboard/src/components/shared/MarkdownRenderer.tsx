import { memo } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkBreaks from 'remark-breaks';
import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter';
import { vs } from 'react-syntax-highlighter/dist/esm/styles/prism';
import { Box } from '@mui/material';
import {
  thoughtMarkdownComponents,
  finalAnswerMarkdownComponents,
  hasMarkdownSyntax
} from '../../utils/markdownComponents';
import CopyButton from './CopyButton';

type MarkdownVariant = 'thinking' | 'response' | 'finalAnalysis' | 'executiveSummary';

interface MarkdownRendererProps {
  content: string;
  variant?: MarkdownVariant;
}

const variantComponents: Record<MarkdownVariant, Record<string, any>> = {
  thinking: thoughtMarkdownComponents,
  response: thoughtMarkdownComponents,
  finalAnalysis: finalAnswerMarkdownComponents,
  executiveSummary: finalAnswerMarkdownComponents,
};

/**
 * MarkdownRenderer component
 * Wrapper around react-markdown with variant-specific component overrides,
 * remark-breaks for line breaks, and syntax-highlighted code blocks.
 */
function MarkdownRenderer({ content, variant = 'response' }: MarkdownRendererProps) {
  // For plain text without markdown syntax, render directly
  if (!hasMarkdownSyntax(content)) {
    const baseComponents = variantComponents[variant];
    const ParagraphComponent = baseComponents.p;
    if (ParagraphComponent) {
      return <ParagraphComponent>{content}</ParagraphComponent>;
    }
    return <>{content}</>;
  }

  const baseComponents = variantComponents[variant];

  // Merge base components with syntax-highlighted code block support
  const components = {
    ...baseComponents,
    // Override code to support syntax-highlighted fenced code blocks
    code: (props: any) => {
      const { node: _node, inline, className, children, ...rest } = props;
      const match = /language-(\w+)/.exec(className || '');

      if (!inline && match) {
        const codeString = String(children).replace(/\n$/, '');
        return (
          <Box sx={{ position: 'relative', my: 1 }}>
            <Box sx={{ position: 'absolute', top: 4, right: 4, zIndex: 1 }}>
              <CopyButton text={codeString} variant="icon" size="small" tooltip="Copy code" />
            </Box>
            <SyntaxHighlighter
              style={vs}
              language={match[1]}
              PreTag="div"
              customStyle={{
                margin: 0,
                borderRadius: '4px',
                fontSize: '0.85rem',
              }}
              {...rest}
            >
              {codeString}
            </SyntaxHighlighter>
          </Box>
        );
      }

      // Fallback to base code component for inline code
      if (baseComponents.code) {
        return baseComponents.code({ ...props });
      }
      return <code className={className} {...rest}>{children}</code>;
    },
  };

  return (
    <ReactMarkdown
      remarkPlugins={[remarkBreaks]}
      components={components}
    >
      {content}
    </ReactMarkdown>
  );
}

export default memo(MarkdownRenderer);
