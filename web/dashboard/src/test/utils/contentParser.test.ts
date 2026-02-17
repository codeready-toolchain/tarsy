/**
 * Tests for contentParser.ts
 *
 * Covers: parseContent, parsePythonLLMMessages, parseMixedContent
 */

import { parseContent, parsePythonLLMMessages, parseMixedContent } from '../../components/shared/JsonDisplay/utils/contentParser';

// ---------------------------------------------------------------------------
// parseContent
// ---------------------------------------------------------------------------

describe('parseContent', () => {
  it('handles null', () => {
    const result = parseContent(null);
    expect(result.type).toBe('plain-text');
    expect(result.content).toBe('null');
  });

  it('handles undefined', () => {
    const result = parseContent(undefined);
    expect(result.type).toBe('plain-text');
    expect(result.content).toBe('undefined');
  });

  describe('object with { result: "..." }', () => {
    it('parses JSON result string', () => {
      const result = parseContent({ result: '{"pods": [{"name": "nginx"}]}' });
      expect(result.type).toBe('mixed');
      expect(result.sections).toBeDefined();
      expect(result.sections![0].title).toContain('MCP Tool Result');
      expect(result.sections![0].type).toBe('json');
    });

    it('detects YAML result', () => {
      const yaml = 'apiVersion: v1\nkind: Pod\nmetadata:\n  name: nginx';
      const result = parseContent({ result: yaml });
      expect(result.type).toBe('mixed');
      expect(result.sections![0].type).toBe('yaml');
      expect(result.sections![0].title).toContain('YAML');
    });

    it('detects structured text result', () => {
      const text = 'This is a longer text output that spans multiple lines\nwith relevant log data\nand more information from the tool';
      const result = parseContent({ result: text });
      expect(result.type).toBe('mixed');
      expect(result.sections![0].type).toBe('text');
    });

    it('extracts multi-line fields from JSON result', () => {
      const bigContent = 'A'.repeat(250) + '\nmore lines';
      const result = parseContent({ result: JSON.stringify({ output: bigContent }) });
      expect(result.sections!.length).toBeGreaterThan(1);
      const textSection = result.sections!.find((s) => s.type === 'text');
      expect(textSection).toBeDefined();
      expect(textSection!.content).toBe(bigContent);
    });

    it('handles simple JSON value in result', () => {
      const result = parseContent({ result: '"hello"' });
      expect(result.type).toBe('mixed');
      expect(result.sections![0].type).toBe('json');
    });

    it('falls through for short non-JSON result', () => {
      const result = parseContent({ result: 'ok' });
      // Single key = 'result', value is a plain short string → recurses via parseContent('ok')
      expect(result.type).toBe('plain-text');
    });
  });

  describe('string values', () => {
    it('parses Python LLMMessage strings', () => {
      const pythonStr = "[LLMMessage(role='system', content='You are helpful')]";
      const result = parseContent(pythonStr);
      expect(result.type).toBe('python-objects');
      expect(result.sections).toBeDefined();
      expect(result.sections![0].type).toBe('system-prompt');
    });

    it('parses JSON strings', () => {
      const result = parseContent('{"key": "value"}');
      expect(result.type).toBe('json');
    });

    it('detects markdown content', () => {
      const result = parseContent('## Heading\n\n**Bold text**');
      // This triggers parseMixedContent because of ## and **
      expect(['mixed', 'markdown']).toContain(result.type);
    });

    it('returns plain-text for simple strings', () => {
      const result = parseContent('hello world');
      expect(result.type).toBe('plain-text');
      expect(result.content).toBe('hello world');
    });

    it('detects YAML content in plain strings', () => {
      const yaml = 'apiVersion: v1\nkind: Service';
      const result = parseContent(yaml);
      expect(result.type).toBe('mixed');
      expect(result.sections![0].type).toBe('yaml');
    });

    it('detects structured multi-line text', () => {
      const text = 'This is a longer text output that has several lines\nof content from a command execution\nshowing various results';
      const result = parseContent(text);
      expect(result.type).toBe('mixed');
      expect(result.sections![0].type).toMatch(/text|yaml/);
    });

    it('detects strings with code blocks', () => {
      const content = 'Here is some code:\n```python\nprint("hello")\n```';
      const result = parseContent(content);
      expect(result.type).toBe('mixed');
    });
  });

  describe('other types', () => {
    it('wraps numbers as json', () => {
      const result = parseContent(42);
      expect(result.type).toBe('json');
      expect(result.content).toBe(42);
    });

    it('wraps booleans as json', () => {
      const result = parseContent(true);
      expect(result.type).toBe('json');
    });

    it('wraps arrays as json', () => {
      const result = parseContent([1, 2, 3]);
      expect(result.type).toBe('json');
    });

    it('wraps objects without "result" as json or recurses', () => {
      const result = parseContent({ foo: 'bar' });
      // Object without result key — treated as json
      expect(result.type).toBe('json');
    });
  });
});

// ---------------------------------------------------------------------------
// parsePythonLLMMessages
// ---------------------------------------------------------------------------

describe('parsePythonLLMMessages', () => {
  it('parses a single system message', () => {
    const content = "[LLMMessage(role='system', content='You are a helpful assistant')]";
    const result = parsePythonLLMMessages(content);
    expect(result.type).toBe('python-objects');
    expect(result.sections).toHaveLength(1);
    expect(result.sections![0].type).toBe('system-prompt');
    expect(result.sections![0].title).toBe('System Message');
    expect(result.sections![0].content).toBe('You are a helpful assistant');
  });

  it('parses multiple messages', () => {
    const content =
      "[LLMMessage(role='system', content='You are a bot'), LLMMessage(role='user', content='Hello')]";
    const result = parsePythonLLMMessages(content);
    expect(result.type).toBe('python-objects');
    expect(result.sections).toHaveLength(2);
    expect(result.sections![0].type).toBe('system-prompt');
    expect(result.sections![1].type).toBe('user-prompt');
  });

  it('handles escaped characters in content', () => {
    const content = "[LLMMessage(role='assistant', content='line1\\nline2\\ttab')]";
    const result = parsePythonLLMMessages(content);
    expect(result.type).toBe('python-objects');
    const msgContent = result.sections![0].content as string;
    expect(msgContent).toContain('line1\nline2\ttab');
  });

  it('handles escaped single quotes', () => {
    const content = "[LLMMessage(role='user', content='it\\'s working')]";
    const result = parsePythonLLMMessages(content);
    expect(result.type).toBe('python-objects');
    const msgContent = result.sections![0].content as string;
    expect(msgContent).toContain("it's working");
  });

  it('classifies assistant messages correctly', () => {
    const content = "[LLMMessage(role='assistant', content='I can help')]";
    const result = parsePythonLLMMessages(content);
    expect(result.sections![0].type).toBe('assistant-prompt');
  });

  it('returns plain-text for malformed input', () => {
    const content = 'not a python message';
    const result = parsePythonLLMMessages(content);
    expect(result.type).toBe('plain-text');
  });

  it('returns plain-text when no role can be extracted', () => {
    const content = "[LLMMessage(norole='system')]";
    const result = parsePythonLLMMessages(content);
    expect(result.type).toBe('plain-text');
  });
});

// ---------------------------------------------------------------------------
// parseMixedContent
// ---------------------------------------------------------------------------

describe('parseMixedContent', () => {
  it('extracts JSON code blocks', () => {
    const content = 'Before\n```json\n{"key": "value"}\n```\nAfter';
    const result = parseMixedContent(content);
    expect(result.type).toBe('mixed');
    expect(result.sections).toBeDefined();
    const jsonSection = result.sections!.find((s) => s.type === 'json');
    expect(jsonSection).toBeDefined();
    expect(jsonSection!.content).toEqual({ key: 'value' });
  });

  it('extracts non-JSON code blocks', () => {
    const content = 'Code:\n```python\nprint("hello")\n```';
    const result = parseMixedContent(content);
    expect(result.type).toBe('mixed');
    const codeSection = result.sections!.find((s) => s.type === 'code');
    expect(codeSection).toBeDefined();
    expect(codeSection!.content).toContain('print("hello")');
  });

  it('detects markdown when no code blocks present', () => {
    const content = '## A heading\n\nSome **bold** text';
    const result = parseMixedContent(content);
    expect(result.type).toBe('markdown');
    expect(result.content).toBe(content);
  });

  it('returns plain-text for simple content', () => {
    const content = 'Just a simple sentence.';
    const result = parseMixedContent(content);
    expect(result.type).toBe('plain-text');
  });

  it('returns plain-text for invalid JSON-only code block', () => {
    const content = '```json\n{invalid json}\n```';
    const result = parseMixedContent(content);
    // Invalid JSON is skipped by the json regex, and the code regex skips 'json' blocks,
    // so no sections are extracted → falls through to plain-text
    expect(result.type).toBe('plain-text');
  });

  it('handles multiple code blocks', () => {
    const content = '```json\n{"a":1}\n```\nText\n```python\nprint(1)\n```';
    const result = parseMixedContent(content);
    expect(result.type).toBe('mixed');
    expect(result.sections!.length).toBeGreaterThanOrEqual(2);
  });
});
