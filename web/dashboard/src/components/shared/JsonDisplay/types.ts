export interface JsonDisplayProps {
  data: any;
  collapsed?: boolean | number;
  maxHeight?: number;
}

export type SectionType = 'json' | 'yaml' | 'code' | 'text' | 'system-prompt' | 'user-prompt' | 'assistant-prompt';

export interface ParsedContent {
  type: 'json' | 'python-objects' | 'markdown' | 'mixed' | 'plain-text';
  content: any;
  sections?: Array<{
    id: string;
    title: string;
    type: SectionType;
    content: any;
    raw: string;
  }>;
}
