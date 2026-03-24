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
 * Apply syntax highlighting to YAML content using CSS classes.
 * Colours are defined in JsonDisplay.css with light/dark overrides via [data-theme].
 */
export const highlightYaml = (yaml: string): string => {
  const lines = yaml.split('\n');
  const highlightedLines = lines.map(line => {
    const leadingSpaces = line.match(/^(\s*)/)?.[1] || '';
    const trimmedLine = line.trimStart();
    
    if (trimmedLine.startsWith('#')) {
      return `${leadingSpaces}<span class="yaml-comment">${escapeHtml(trimmedLine)}</span>`;
    }
    
    const keyValueMatch = trimmedLine.match(/^([^:]+):\s*(.*)$/);
    if (keyValueMatch) {
      const key = keyValueMatch[1];
      const value = keyValueMatch[2];
      
      let highlightedValue = escapeHtml(value);
      
      if (value === 'null' || value === '~') {
        highlightedValue = `<span class="yaml-null">${escapeHtml(value)}</span>`;
      } else if (value === 'true' || value === 'false') {
        highlightedValue = `<span class="yaml-boolean">${escapeHtml(value)}</span>`;
      } else if (/^-?\d+(\.\d+)?$/.test(value.trim())) {
        highlightedValue = `<span class="yaml-number">${escapeHtml(value)}</span>`;
      } else if (value.startsWith('"') && value.endsWith('"')) {
        highlightedValue = `<span class="yaml-string">${escapeHtml(value)}</span>`;
      } else if (value.startsWith("'") && value.endsWith("'")) {
        highlightedValue = `<span class="yaml-string">${escapeHtml(value)}</span>`;
      } else if (value.trim() && !value.startsWith('-') && !value.startsWith('[') && !value.startsWith('{')) {
        highlightedValue = `<span class="yaml-string">${escapeHtml(value)}</span>`;
      }
      
      return `${leadingSpaces}<span class="yaml-key">${escapeHtml(key)}</span>: ${highlightedValue}`;
    }
    
    if (trimmedLine.startsWith('- ')) {
      const content = trimmedLine.substring(2);
      return `${leadingSpaces}<span class="yaml-list-marker">-</span> ${escapeHtml(content)}`;
    }
    
    return `${leadingSpaces}${escapeHtml(trimmedLine)}`;
  });
  
  return highlightedLines.join('\n');
};
