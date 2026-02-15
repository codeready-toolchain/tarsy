export interface JsonDisplayProps {
  data: unknown;
  collapsed?: boolean | number;
  maxHeight?: number;
}

export type SectionType = 'json' | 'yaml' | 'code' | 'text' | 'system-prompt' | 'user-prompt' | 'assistant-prompt';

export interface ContentSection {
  id: string;
  title: string;
  type: SectionType;
  content: unknown;
  raw: string;
}

export interface ParsedContent {
  type: 'json' | 'python-objects' | 'markdown' | 'mixed' | 'plain-text';
  content: unknown;
  sections?: ContentSection[];
}
