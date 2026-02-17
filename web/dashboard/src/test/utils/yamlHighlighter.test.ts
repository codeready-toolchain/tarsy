/**
 * Tests for yamlHighlighter.ts
 *
 * Covers: escapeHtml (via highlighting output), highlightYaml
 */

import { highlightYaml } from '../../components/shared/JsonDisplay/utils/yamlHighlighter';

// ---------------------------------------------------------------------------
// highlightYaml
// ---------------------------------------------------------------------------

describe('highlightYaml', () => {
  describe('key-value pairs', () => {
    it('highlights keys', () => {
      const result = highlightYaml('name: nginx');
      expect(result).toContain('color: #5E81AC');
      expect(result).toContain('name');
    });

    it('highlights string values', () => {
      const result = highlightYaml('name: nginx');
      expect(result).toContain('color: #7FAF6E');
      expect(result).toContain('nginx');
    });

    it('highlights quoted string values', () => {
      const result = highlightYaml('name: "nginx"');
      expect(result).toContain('color: #7FAF6E');
    });

    it('highlights single-quoted string values', () => {
      const result = highlightYaml("name: 'nginx'");
      expect(result).toContain('color: #7FAF6E');
    });

    it('highlights numeric values', () => {
      const result = highlightYaml('replicas: 3');
      expect(result).toContain('color: #9570A0');
      expect(result).toContain('3');
    });

    it('highlights negative numeric values', () => {
      const result = highlightYaml('offset: -5');
      expect(result).toContain('color: #9570A0');
    });

    it('highlights float values', () => {
      const result = highlightYaml('ratio: 0.75');
      expect(result).toContain('color: #9570A0');
    });

    it('highlights boolean true', () => {
      const result = highlightYaml('enabled: true');
      expect(result).toContain('color: #9570A0');
    });

    it('highlights boolean false', () => {
      const result = highlightYaml('enabled: false');
      expect(result).toContain('color: #9570A0');
    });

    it('highlights null values', () => {
      const result = highlightYaml('value: null');
      expect(result).toContain('color: #BF616A');
    });

    it('highlights tilde as null', () => {
      const result = highlightYaml('value: ~');
      expect(result).toContain('color: #BF616A');
    });

    it('does not highlight empty values', () => {
      const result = highlightYaml('key:');
      // Key is highlighted but no value color
      expect(result).toContain('color: #5E81AC');
    });
  });

  describe('list items', () => {
    it('highlights list marker', () => {
      const result = highlightYaml('- item1');
      expect(result).toContain('color: #5E9DB8');
      expect(result).toContain('item1');
    });

    it('handles indented list items', () => {
      const result = highlightYaml('  - nested');
      expect(result).toContain('color: #5E9DB8');
      expect(result).toContain('nested');
    });
  });

  describe('comments', () => {
    it('highlights comment lines', () => {
      const result = highlightYaml('# This is a comment');
      expect(result).toContain('color: #4C566A');
      expect(result).toContain('font-style: italic');
    });

    it('handles indented comments', () => {
      const result = highlightYaml('  # indented comment');
      expect(result).toContain('color: #4C566A');
    });
  });

  describe('multi-line YAML', () => {
    it('highlights complete YAML document', () => {
      const yaml = `apiVersion: v1
kind: Pod
metadata:
  name: nginx
  labels:
    app: web
spec:
  replicas: 3
  enabled: true
  timeout: null`;

      const result = highlightYaml(yaml);
      // Keys
      expect(result).toContain('apiVersion');
      expect(result).toContain('kind');
      // String values
      expect(result).toContain('v1');
      expect(result).toContain('Pod');
      // Numeric
      expect(result).toContain('3');
      // Boolean
      expect(result).toContain('true');
      // Null
      expect(result).toContain('null');
    });
  });

  describe('HTML escaping (XSS prevention)', () => {
    it('escapes angle brackets in keys', () => {
      const result = highlightYaml('<script>: value');
      expect(result).toContain('&lt;script&gt;');
      expect(result).not.toContain('<script>');
    });

    it('escapes angle brackets in values', () => {
      const result = highlightYaml('name: <img onerror=alert(1)>');
      expect(result).toContain('&lt;img');
      expect(result).not.toContain('<img');
    });

    it('escapes ampersands', () => {
      const result = highlightYaml('query: foo&bar');
      expect(result).toContain('&amp;');
    });

    it('escapes quotes', () => {
      const result = highlightYaml('name: say "hello"');
      // The double quotes are part of the value, not a quoted-string pattern
      expect(result).toContain('&quot;');
    });

    it('escapes in comments', () => {
      const result = highlightYaml('# <script>alert("xss")</script>');
      expect(result).toContain('&lt;script&gt;');
      expect(result).not.toContain('<script>');
    });

    it('escapes in list items', () => {
      const result = highlightYaml('- <b>bold</b>');
      expect(result).toContain('&lt;b&gt;');
    });
  });

  describe('edge cases', () => {
    it('handles empty string', () => {
      expect(highlightYaml('')).toBe('');
    });

    it('handles plain text lines', () => {
      const result = highlightYaml('just plain text');
      expect(result).toContain('just plain text');
    });

    it('preserves indentation', () => {
      const result = highlightYaml('    name: nginx');
      expect(result.startsWith('    ')).toBe(true);
    });
  });
});
