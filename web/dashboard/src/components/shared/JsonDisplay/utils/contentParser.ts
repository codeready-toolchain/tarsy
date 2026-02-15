import type { ParsedContent, SectionType } from '../types';

/**
 * Parse Python LLMMessage objects from string representation
 */
export const parsePythonLLMMessages = (content: string): ParsedContent => {
  try {
    const messages: Array<{ role: string; content: string }> = [];
    const messageParts = content.split('LLMMessage(').slice(1);
    
    messageParts.forEach((part) => {
      const roleMatch = part.match(/role='([^']+)'/);
      if (!roleMatch) return;
      
      const role = roleMatch[1];
      const contentStartMatch = part.match(/content='(.*)$/s);
      if (!contentStartMatch) return;
      
      const rawContent = contentStartMatch[1];
      let messageContent = '';
      let i = 0;
      let escapeNext = false;
      
      while (i < rawContent.length) {
        const char = rawContent[i];
        
        if (escapeNext) {
          messageContent += char;
          escapeNext = false;
        } else if (char === '\\') {
          messageContent += char;
          escapeNext = true;
        } else if (char === "'") {
          const nextChars = rawContent.substring(i + 1, i + 5);
          if (nextChars.startsWith(')') || nextChars.match(/^,\s*[a-zA-Z_]+=/) || i === rawContent.length - 1) {
            break;
          }
          messageContent += char;
        } else {
          messageContent += char;
        }
        i++;
      }
      
      messageContent = messageContent
        .replace(/\\n/g, '\n')
        .replace(/\\'/g, "'")
        .replace(/\\"/g, '"')
        .replace(/\\\\/g, '\\')
        .replace(/\\t/g, '\t');
      
      messages.push({ role, content: messageContent });
    });

    if (messages.length > 0) {
      const sections = messages.map((msg, index) => {
        let sectionType: SectionType;
        if (msg.role === 'system') sectionType = 'system-prompt';
        else if (msg.role === 'assistant') sectionType = 'assistant-prompt';
        else sectionType = 'user-prompt';
        
        return {
          id: `llm-message-${msg.role}-${index}`,
          title: `${msg.role.charAt(0).toUpperCase() + msg.role.slice(1)} Message`,
          type: sectionType,
          content: msg.content,
          raw: `Role: ${msg.role}\n\n${msg.content}`
        };
      });

      return { type: 'python-objects', content: messages, sections };
    }
  } catch (error) {
    console.warn('Failed to parse Python LLMMessage objects:', error);
  }

  return { type: 'plain-text', content };
};

/**
 * Parse mixed content with JSON snippets, markdown, code blocks, etc.
 */
export const parseMixedContent = (content: string): ParsedContent => {
  const sections = [];
  let remainingContent = content;
  let sectionIndex = 0;

  // Extract JSON code blocks
  const jsonRegex = /```json\s*([\s\S]*?)\s*```/g;
  let jsonMatch;
  while ((jsonMatch = jsonRegex.exec(content)) !== null) {
    try {
      const jsonContent = JSON.parse(jsonMatch[1]);
      sections.push({
        id: `json-block-${sectionIndex + 1}`,
        title: `JSON Block ${sectionIndex + 1}`,
        type: 'json' as SectionType,
        content: jsonContent,
        raw: jsonMatch[1]
      });
      sectionIndex++;
      remainingContent = remainingContent.replace(jsonMatch[0], `[JSON_BLOCK_${sectionIndex}]`);
    } catch {
      // Invalid JSON, skip
    }
  }

  // Extract other code blocks
  const codeRegex = /```(\w*)\s*([\s\S]*?)\s*```/g;
  let codeMatch;
  while ((codeMatch = codeRegex.exec(content)) !== null) {
    if (codeMatch[1] !== 'json') {
      sections.push({
        id: `code-block-${sectionIndex + 1}`,
        title: `${codeMatch[1] || 'Code'} Block ${sectionIndex + 1}`,
        type: 'code' as SectionType,
        content: codeMatch[2],
        raw: codeMatch[2]
      });
      sectionIndex++;
      remainingContent = remainingContent.replace(codeMatch[0], `[CODE_BLOCK_${sectionIndex}]`);
    }
  }

  if (sections.length > 0) {
    return { type: 'mixed', content: { text: remainingContent, sections }, sections };
  }

  if (content.includes('##') || content.includes('**') || content.includes('- ')) {
    return { type: 'markdown', content };
  }

  return { type: 'plain-text', content };
};

/**
 * Main content parser - intelligently detects and parses different content types
 */
export const parseContent = (value: unknown): ParsedContent => {
  if (value === null || value === undefined) {
    return { type: 'plain-text', content: String(value) };
  }

  if (typeof value === 'object' && value !== null) {
    if ('result' in value && typeof value.result === 'string') {
      const resultContent = value.result.trim();
      
      try {
        const parsedJson = JSON.parse(resultContent);
        
        if (typeof parsedJson === 'object' && parsedJson !== null) {
          const multiLineFields: Array<{ path: string; fieldName: string; content: string }> = [];
          
          const findMultiLineFields = (obj: unknown, path: string[] = []) => {
            if (typeof obj === 'string' && obj.length > 200 && obj.includes('\n')) {
              const fieldName = path[path.length - 1] || 'content';
              multiLineFields.push({ path: path.join(' → '), fieldName, content: obj });
            } else if (Array.isArray(obj)) {
              obj.forEach((item, index) => findMultiLineFields(item, [...path, `[${index}]`]));
            } else if (typeof obj === 'object' && obj !== null) {
              for (const [key, val] of Object.entries(obj)) {
                findMultiLineFields(val, [...path, key]);
              }
            }
          };
          
          findMultiLineFields(parsedJson);
          
          if (multiLineFields.length > 0) {
            const sections = [
              {
                id: 'mcp-json',
                title: 'MCP Tool Result (JSON)',
                type: 'json' as SectionType,
                content: parsedJson,
                raw: JSON.stringify(parsedJson, null, 2)
              }
            ];
            
            for (const { path, fieldName, content } of multiLineFields) {
              sections.push({
                id: `mlf:${path || fieldName}`,
                title: `${fieldName.charAt(0).toUpperCase() + fieldName.slice(1)} (Formatted)`,
                type: 'text' as SectionType,
                content: content,
                raw: content
              });
            }
            
            return { type: 'mixed', content: { text: '', sections: [] }, sections };
          }
          
          return {
            type: 'mixed',
            content: { text: '', sections: [] },
            sections: [{
              id: 'mcp-json',
              title: 'MCP Tool Result (JSON)',
              type: 'json' as SectionType,
              content: parsedJson,
              raw: JSON.stringify(parsedJson, null, 2)
            }]
          };
        }
        
        return {
          type: 'mixed',
          content: { text: '', sections: [] },
          sections: [{
            id: 'mcp-json-simple',
            title: 'MCP Tool Result',
            type: 'json' as SectionType,
            content: parsedJson,
            raw: JSON.stringify(parsedJson, null, 2)
          }]
        };
      } catch {
        // Not valid JSON, try other formats
      }
      
      if (resultContent.includes('apiVersion:') || 
          resultContent.includes('kind:') || 
          resultContent.includes('metadata:') ||
          (resultContent.includes('\n') && (resultContent.includes(':') || resultContent.includes('-')))) {
        return {
          type: 'mixed',
          content: { text: '', sections: [] },
          sections: [{
            id: 'mcp-yaml',
            title: 'MCP Tool Result (YAML)',
            type: 'yaml' as SectionType,
            content: resultContent,
            raw: resultContent
          }]
        };
      }
      
      if (resultContent.length > 50 && (resultContent.includes('\n') || resultContent.includes('\t'))) {
        return {
          type: 'mixed',
          content: { text: '', sections: [] },
          sections: [{
            id: 'mcp-text',
            title: 'MCP Tool Result (Text)',
            type: 'text' as SectionType,
            content: resultContent,
            raw: resultContent
          }]
        };
      }
    }
    
    const keys = Object.keys(value);
    if (keys.length === 1 && keys[0] === 'result') {
      return parseContent((value as Record<string, unknown>).result);
    }
  }

  if (typeof value === 'string') {
    const content = value.trim();
    
    if (content.startsWith('[') && content.includes('LLMMessage(') && content.includes('role=')) {
      return parsePythonLLMMessages(content);
    }
    
    try {
      const parsed = JSON.parse(content);
      if (typeof parsed === 'object') {
        return { type: 'json', content: parsed };
      }
    } catch {
      // Not pure JSON
    }
    
    // Check for YAML content (Kubernetes resources, structured config, etc.)
    // This mirrors the detection in the object { result: "..." } branch above,
    // needed because tool results may arrive as plain strings rather than wrapped objects.
    if (content.includes('apiVersion:') ||
        content.includes('kind:') ||
        content.includes('metadata:') ||
        (content.includes('\n') && (content.includes(':') || content.includes('-')))) {
      return {
        type: 'mixed',
        content: { text: '', sections: [] },
        sections: [{
          id: 'mcp-yaml',
          title: 'MCP Tool Result (YAML)',
          type: 'yaml' as SectionType,
          content: content,
          raw: content,
        }],
      };
    }

    // Check for structured text content (multi-line output, logs, etc.)
    if (content.length > 50 && (content.includes('\n') || content.includes('\t'))) {
      return {
        type: 'mixed',
        content: { text: '', sections: [] },
        sections: [{
          id: 'mcp-text',
          title: 'MCP Tool Result (Text)',
          type: 'text' as SectionType,
          content: content,
          raw: content,
        }],
      };
    }

    const jsonMatches = content.match(/```json\s*([\s\S]*?)\s*```/g);
    const codeMatches = content.match(/```\w*\s*([\s\S]*?)\s*```/g);
    
    if (jsonMatches || codeMatches || content.includes('##') || content.includes('**')) {
      return parseMixedContent(content);
    }
    
    return { type: 'plain-text', content };
  }

  return { type: 'json', content: value };
};
