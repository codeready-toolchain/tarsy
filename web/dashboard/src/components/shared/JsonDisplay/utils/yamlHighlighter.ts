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

/**
 * Apply syntax highlighting to YAML content
 * 
 * Color palette based on the Nord theme with enhanced contrast:
 * - Keys: #5E81AC (bold) - cool, clear structural element
 * - String values: #7FAF6E (bold) - enhanced readability
 * - Numbers/booleans: #9570A0 (bold) - stronger distinction
 * - Null values: #BF616A (bold) - clear indication
 * - List markers: #5E9DB8 - harmonious structural element
 * - Comments: #4C566A (italic) - muted, unobtrusive
 */
export const highlightYaml = (yaml: string): string => {
  const lines = yaml.split('\n');
  const highlightedLines = lines.map(line => {
    const leadingSpaces = line.match(/^(\s*)/)?.[1] || '';
    const trimmedLine = line.trimStart();
    
    // Comment lines
    if (trimmedLine.startsWith('#')) {
      return `${leadingSpaces}<span style="color: #4C566A; font-style: italic;">${escapeHtml(trimmedLine)}</span>`;
    }
    
    // Key-value pairs
    const keyValueMatch = trimmedLine.match(/^([^:]+):\s*(.*)$/);
    if (keyValueMatch) {
      const key = keyValueMatch[1];
      const value = keyValueMatch[2];
      
      let highlightedValue = escapeHtml(value);
      
      if (value === 'null' || value === '~') {
        highlightedValue = `<span style="color: #BF616A; font-weight: 600;">${escapeHtml(value)}</span>`;
      } else if (value === 'true' || value === 'false') {
        highlightedValue = `<span style="color: #9570A0; font-weight: 600;">${escapeHtml(value)}</span>`;
      } else if (/^-?\d+(\.\d+)?$/.test(value.trim())) {
        highlightedValue = `<span style="color: #9570A0; font-weight: 600;">${escapeHtml(value)}</span>`;
      } else if (value.startsWith('"') && value.endsWith('"')) {
        highlightedValue = `<span style="color: #7FAF6E; font-weight: 600;">${escapeHtml(value)}</span>`;
      } else if (value.startsWith("'") && value.endsWith("'")) {
        highlightedValue = `<span style="color: #7FAF6E; font-weight: 600;">${escapeHtml(value)}</span>`;
      } else if (value.trim() && !value.startsWith('-') && !value.startsWith('[') && !value.startsWith('{')) {
        highlightedValue = `<span style="color: #7FAF6E; font-weight: 600;">${escapeHtml(value)}</span>`;
      }
      
      return `${leadingSpaces}<span style="color: #5E81AC; font-weight: 700;">${escapeHtml(key)}</span>: ${highlightedValue}`;
    }
    
    // List items
    if (trimmedLine.startsWith('- ')) {
      const content = trimmedLine.substring(2);
      return `${leadingSpaces}<span style="color: #5E9DB8; font-weight: 600;">-</span> ${escapeHtml(content)}`;
    }
    
    return `${leadingSpaces}${escapeHtml(trimmedLine)}`;
  });
  
  return highlightedLines.join('\n');
};
