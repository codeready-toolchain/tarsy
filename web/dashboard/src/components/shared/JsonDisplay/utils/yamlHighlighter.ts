/**
 * Escape HTML to prevent XSS attacks
 */
const escapeHtml = (text: string): string => {
  if (!text) return '';

  const htmlEntities: Record<string, string> = {
    '&': '&amp;',
    '<': '&lt;',
    '>': '&gt;',
    '"': '&quot;',
    "'": '&#39;'
  };

  return text.replace(/[&<>"']/g, (char) => htmlEntities[char]);
};

/** `|`, `|-`, `>2` … — a value that starts a multi-line block scalar. */
const isBlockScalarIndicator = (value: string): boolean => /^[|>][+-]?\d*$/.test(value.trim());

const isQuoted = (value: string): boolean =>
  value.length >= 2
  && ((value.startsWith('"') && value.endsWith('"'))
    || (value.startsWith("'") && value.endsWith("'")));

/**
 * Walk a line's characters, tracking quote state, until `predicate` matches.
 * Returns the index or -1. Used so colons/hashes inside quoted scalars are ignored.
 */
const findOutsideQuotes = (
  text: string,
  predicate: (char: string, index: number) => boolean,
): number => {
  let quote: '"' | "'" | null = null;
  for (let i = 0; i < text.length; i++) {
    const char = text[i];
    if (quote === '"') {
      if (char === '\\') {
        i++;
      } else if (char === '"') {
        quote = null;
      }
      continue;
    }
    if (quote === "'") {
      if (char === "'") {
        if (text[i + 1] === "'") {
          i++; // escaped single quote inside a single-quoted scalar
        } else {
          quote = null;
        }
      }
      continue;
    }
    // Quotes only open a scalar at the start of a token; an apostrophe in
    // `it's` must not swallow the rest of the line.
    const previous = text[i - 1];
    if ((char === '"' || char === "'")
      && (i === 0 || previous === undefined || /[\s:[{,}]/.test(previous))) {
      quote = char;
      continue;
    }
    if (predicate(char, i)) return i;
  }
  return -1;
};

/**
 * Split `key: value` on the first mapping colon (colon followed by whitespace
 * or end-of-line, outside quotes). Returns null for plain scalars such as
 * `- kubernetes` or `http://example.com`.
 */
const splitKeyValue = (text: string): { key: string; value: string } | null => {
  const colon = findOutsideQuotes(
    text,
    (char, index) => char === ':' && (text[index + 1] === undefined || /\s/.test(text[index + 1])),
  );
  if (colon <= 0) return null;
  return {
    key: text.slice(0, colon),
    value: text.slice(colon + 1).replace(/^\s+/, ''),
  };
};

/** Separate a trailing `# comment` from an unquoted value. */
const splitInlineComment = (value: string): { value: string; comment: string } => {
  const hash = findOutsideQuotes(
    value,
    (char, index) => char === '#' && (index === 0 || /\s/.test(value[index - 1])),
  );
  if (hash < 0) return { value: value.trimEnd(), comment: '' };
  return { value: value.slice(0, hash).trimEnd(), comment: value.slice(hash) };
};

const renderBareScalar = (value: string): string => {
  if (value === '') return '';
  if (value === 'null' || value === '~' || /^(Null|NULL)$/.test(value)) {
    return `<span class="yaml-null">${escapeHtml(value)}</span>`;
  }
  if (/^(true|false|True|False|TRUE|FALSE)$/.test(value)) {
    return `<span class="yaml-boolean">${escapeHtml(value)}</span>`;
  }
  if (
    /^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$/.test(value)
    || /^[-+]?\.(?:inf|Inf|INF)$/.test(value)
    || /^\.(?:nan|NaN|NAN)$/.test(value)
  ) {
    return `<span class="yaml-number">${escapeHtml(value)}</span>`;
  }
  return `<span class="yaml-string">${escapeHtml(value)}</span>`;
};

/**
 * Highlight a flow collection (`[a, b]`, `{a: b}`). Punctuation is dimmed and
 * scalars keep their type colours; bare keys in flow maps are labelled as keys.
 */
const highlightFlow = (value: string): string => {
  const tokens = value.match(/"(?:[^"\\]|\\.)*"|'(?:[^']|'')*'|[[\]{},:]|[^\s[\]{},:]+|\s+/g);
  if (!tokens) return `<span class="yaml-string">${escapeHtml(value)}</span>`;

  return tokens.map((token, index) => {
    if (/^\s+$/.test(token)) return token;
    if (/^[[\]{},:]$/.test(token)) {
      return `<span class="yaml-punctuation">${escapeHtml(token)}</span>`;
    }
    if (/^["']/.test(token)) {
      return `<span class="yaml-string">${escapeHtml(token)}</span>`;
    }
    let next: string | undefined;
    for (let i = index + 1; i < tokens.length; i++) {
      if (!/^\s+$/.test(tokens[i])) {
        next = tokens[i];
        break;
      }
    }
    if (next === ':') {
      return `<span class="yaml-key">${escapeHtml(token)}</span>`;
    }
    return renderBareScalar(token);
  }).join('');
};

const renderScalar = (value: string): string => {
  if (value === '') return '';
  if (isBlockScalarIndicator(value)) {
    return `<span class="yaml-block-scalar">${escapeHtml(value)}</span>`;
  }
  if (isQuoted(value)) {
    return `<span class="yaml-string">${escapeHtml(value)}</span>`;
  }
  if (
    (value.startsWith('[') && value.endsWith(']'))
    || (value.startsWith('{') && value.endsWith('}'))
  ) {
    return highlightFlow(value);
  }
  if ((value.startsWith('&') || value.startsWith('*')) && !/\s/.test(value)) {
    return `<span class="yaml-anchor">${escapeHtml(value)}</span>`;
  }
  return renderBareScalar(value);
};

const highlightValue = (raw: string): string => {
  const { value, comment } = splitInlineComment(raw);
  const commentHtml = comment
    ? ` <span class="yaml-comment">${escapeHtml(comment)}</span>`
    : '';
  return `${renderScalar(value)}${commentHtml}`;
};

const renderMapping = (key: string, value: string): string =>
  `<span class="yaml-key">${escapeHtml(key)}</span>:${value === '' ? '' : ` ${highlightValue(value)}`}`;

/** Highlight the remainder of a `- ` list item (scalar, nested list, or mapping). */
const highlightListItemContent = (
  content: string,
  onBlockScalar: () => void,
): string => {
  if (content === '') return '';
  if (isBlockScalarIndicator(content)) {
    onBlockScalar();
    return renderScalar(content);
  }
  const nested = content.match(/^(-\s+)(.*)$/);
  if (nested) {
    return `<span class="yaml-list-marker">-</span> ${highlightListItemContent(nested[2], onBlockScalar)}`;
  }
  const mapping = splitKeyValue(content);
  if (mapping) {
    if (isBlockScalarIndicator(mapping.value)) onBlockScalar();
    return renderMapping(mapping.key, mapping.value);
  }
  return renderScalar(content);
};

/**
 * Apply syntax highlighting to YAML content using CSS classes.
 * Colours are defined in JsonDisplay.css with light/dark overrides via [data-theme].
 *
 * Handles nested mappings, list items (including `- key: value`), flow
 * collections, comments, document markers, anchors, and block scalars.
 */
export const highlightYaml = (yaml: string): string => {
  let blockScalarIndent: number | null = null;

  const highlightedLines = yaml.split('\n').map((line) => {
    const leadingSpaces = line.match(/^(\s*)/)?.[1] ?? '';
    const trimmedLine = line.slice(leadingSpaces.length);
    const indent = leadingSpaces.length;

    // Block scalar bodies run until a non-empty line dedents to or past the key.
    if (blockScalarIndent !== null) {
      if (trimmedLine === '') return line;
      if (indent > blockScalarIndent) {
        return `${leadingSpaces}<span class="yaml-string">${escapeHtml(trimmedLine)}</span>`;
      }
      blockScalarIndent = null;
    }

    if (trimmedLine === '') return line;

    if (trimmedLine.startsWith('#')) {
      return `${leadingSpaces}<span class="yaml-comment">${escapeHtml(trimmedLine)}</span>`;
    }

    if (/^(---|\.\.\.)\s*$/.test(trimmedLine)) {
      return `${leadingSpaces}<span class="yaml-punctuation">${escapeHtml(trimmedLine)}</span>`;
    }

    if (trimmedLine === '-') {
      return `${leadingSpaces}<span class="yaml-list-marker">-</span>`;
    }

    const listItem = trimmedLine.match(/^-\s+(.*)$/);
    if (listItem) {
      const content = highlightListItemContent(listItem[1], () => {
        blockScalarIndent = indent;
      });
      return `${leadingSpaces}<span class="yaml-list-marker">-</span> ${content}`;
    }

    const mapping = splitKeyValue(trimmedLine);
    if (mapping) {
      if (isBlockScalarIndicator(mapping.value)) blockScalarIndent = indent;
      return `${leadingSpaces}${renderMapping(mapping.key, mapping.value)}`;
    }

    return `${leadingSpaces}${escapeHtml(trimmedLine)}`;
  });

  return highlightedLines.join('\n');
};
