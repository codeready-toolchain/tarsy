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
      expect(result).toContain('class="yaml-key"');
      expect(result).toContain('name');
    });

    it('highlights string values', () => {
      const result = highlightYaml('name: nginx');
      expect(result).toContain('class="yaml-string"');
      expect(result).toContain('nginx');
    });

    it('highlights quoted string values', () => {
      const result = highlightYaml('name: "nginx"');
      expect(result).toContain('class="yaml-string"');
    });

    it('highlights single-quoted string values', () => {
      const result = highlightYaml("name: 'nginx'");
      expect(result).toContain('class="yaml-string"');
    });

    it('highlights numeric values', () => {
      const result = highlightYaml('replicas: 3');
      expect(result).toContain('class="yaml-number"');
      expect(result).toContain('3');
    });

    it('highlights negative numeric values', () => {
      const result = highlightYaml('offset: -5');
      expect(result).toContain('class="yaml-number"');
    });

    it('highlights float values', () => {
      const result = highlightYaml('ratio: 0.75');
      expect(result).toContain('class="yaml-number"');
    });

    it('highlights boolean true', () => {
      const result = highlightYaml('enabled: true');
      expect(result).toContain('class="yaml-boolean"');
    });

    it('highlights boolean false', () => {
      const result = highlightYaml('enabled: false');
      expect(result).toContain('class="yaml-boolean"');
    });

    it('highlights null values', () => {
      const result = highlightYaml('value: null');
      expect(result).toContain('class="yaml-null"');
    });

    it('highlights tilde as null', () => {
      const result = highlightYaml('value: ~');
      expect(result).toContain('class="yaml-null"');
    });

    it('does not highlight empty values', () => {
      const result = highlightYaml('key:');
      expect(result).toContain('class="yaml-key"');
    });
  });

  describe('list items', () => {
    it('highlights list marker', () => {
      const result = highlightYaml('- item1');
      expect(result).toContain('class="yaml-list-marker"');
      expect(result).toContain('item1');
    });

    it('handles indented list items', () => {
      const result = highlightYaml('  - nested');
      expect(result).toContain('class="yaml-list-marker"');
      expect(result).toContain('nested');
    });
  });

  describe('comments', () => {
    it('highlights comment lines', () => {
      const result = highlightYaml('# This is a comment');
      expect(result).toContain('class="yaml-comment"');
    });

    it('handles indented comments', () => {
      const result = highlightYaml('  # indented comment');
      expect(result).toContain('class="yaml-comment"');
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

  describe('list items with mappings', () => {
    it('highlights the key and value of a `- key: value` item', () => {
      const result = highlightYaml('- name: nginx');
      expect(result).toContain('class="yaml-list-marker"');
      expect(result).toContain('class="yaml-key">name');
      expect(result).toContain('class="yaml-string">nginx');
    });

    it('colours a plain scalar list item as a string', () => {
      const result = highlightYaml('servers:\n  - kubernetes');
      expect(result).toContain('class="yaml-string">kubernetes');
    });

    it('does not treat a URL list item as a mapping', () => {
      const result = highlightYaml('- http://example.com');
      expect(result).not.toContain('class="yaml-key"');
      expect(result).toContain('class="yaml-string">http://example.com');
    });
  });

  describe('inline comments', () => {
    it('separates a trailing comment from the value', () => {
      const result = highlightYaml('name: nginx # web server');
      expect(result).toContain('class="yaml-string">nginx');
      expect(result).toContain('class="yaml-comment"># web server');
    });

    it('keeps a hash inside a quoted value', () => {
      const result = highlightYaml('name: "a # b"');
      expect(result).not.toContain('class="yaml-comment"');
      expect(result).toContain('class="yaml-string"');
    });
  });

  describe('flow collections', () => {
    it('dims brackets and commas while colouring scalars', () => {
      const result = highlightYaml('servers: [a, b]');
      expect(result).toContain('class="yaml-punctuation">[');
      expect(result).toContain('class="yaml-punctuation">,');
      expect(result).toContain('class="yaml-string">a');
      expect(result).toContain('class="yaml-string">b');
    });

    it('colours bare keys in flow maps', () => {
      const result = highlightYaml('limits: {cpu: 2, memory: 4}');
      expect(result).toContain('class="yaml-key">cpu');
      expect(result).toContain('class="yaml-number">2');
      expect(result).toContain('class="yaml-key">memory');
      expect(result).toContain('class="yaml-number">4');
    });
  });

  describe('block scalars', () => {
    it('colours the indicator and the indented body', () => {
      const yaml = `instructions: |-
  line one
  line two
next: value`;
      const result = highlightYaml(yaml);
      expect(result).toContain('class="yaml-block-scalar">|-');
      expect(result).toContain('class="yaml-string">line one');
      expect(result).toContain('class="yaml-string">line two');
      expect(result).toContain('class="yaml-key">next');
    });

    it('ends the block at the next dedented key', () => {
      const result = highlightYaml('body: |\n  text\nsibling: 3');
      expect(result).toContain('class="yaml-number">3');
      expect(result).not.toContain('class="yaml-string">text\nsibling');
    });
  });

  describe('document markers and anchors', () => {
    it('dims a document marker', () => {
      expect(highlightYaml('---')).toContain('class="yaml-punctuation">---');
    });

    it('colours an alias', () => {
      expect(highlightYaml('value: *defaults')).toContain('class="yaml-anchor">*defaults');
    });
  });

  describe('keys containing colons', () => {
    it('splits on the colon outside a quoted key', () => {
      const result = highlightYaml('"a: b": value');
      expect(result).toContain('class="yaml-key">&quot;a: b&quot;');
      expect(result).toContain('class="yaml-string">value');
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
