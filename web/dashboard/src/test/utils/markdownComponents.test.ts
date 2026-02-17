/**
 * Tests for markdownComponents.tsx
 *
 * Covers: hasMarkdownSyntax
 */

import { hasMarkdownSyntax } from '../../utils/markdownComponents';

describe('hasMarkdownSyntax', () => {
  it.each([
    ['**bold text**', true],
    ['*italic text*', true],
    ['`inline code`', true],
    ['[link text](url)', true],
    ['# Heading', true],
    ['## Sub-heading', true],
    ['- list item', true],
    ['_underscored_', true],
    ['some **mixed** content', true],
    ['```code block```', true],
  ])('detects markdown in "%s" → %s', (text, expected) => {
    expect(hasMarkdownSyntax(text)).toBe(expected);
  });

  it.each([
    ['plain text without markdown', false],
    ['Hello World', false],
    ['Just a number: 42', false],
    ['No special chars here', false],
    ['Simple sentence.', false],
  ])('does not detect markdown in "%s" → %s', (text, expected) => {
    expect(hasMarkdownSyntax(text)).toBe(expected);
  });

  it('detects single special characters', () => {
    expect(hasMarkdownSyntax('*')).toBe(true);
    expect(hasMarkdownSyntax('#')).toBe(true);
    expect(hasMarkdownSyntax('`')).toBe(true);
    expect(hasMarkdownSyntax('-')).toBe(true);
  });

  it('handles empty string', () => {
    expect(hasMarkdownSyntax('')).toBe(false);
  });
});
