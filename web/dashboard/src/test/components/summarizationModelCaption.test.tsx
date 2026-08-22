import { render, screen } from '@testing-library/react';
import type { ReactElement } from 'react';
import { ThemeProvider, createTheme } from '@mui/material/styles';
import ToolSummaryItem from '../../components/timeline/ToolSummaryItem';
import ToolCallItem from '../../components/timeline/ToolCallItem';
import SummarizationModelCaption, { summarizationModelLabel } from '../../components/timeline/SummarizationModelCaption';
import { FLOW_ITEM, type FlowItem } from '../../utils/timelineParser';
import { TIMELINE_STATUS } from '../../constants/eventTypes';
import { TOOL_TYPE, MEMORY_TOOL_NAME } from '../../constants/toolTypes';

vi.mock('../../config/env.ts', () => ({
  config: { isDevelopment: false, isProduction: true },
  DASHBOARD_VERSION: 'test',
  urls: {
    api: { base: '', health: '/health' },
    websocket: { base: 'ws://localhost:8080', path: '/api/v1/ws' },
    oauth: { signIn: '/oauth2/sign_in', signOut: '/oauth2/sign_out', userInfo: '/oauth2/userinfo' },
  },
}));

const theme = createTheme();

function renderWithTheme(ui: ReactElement) {
  return render(<ThemeProvider theme={theme}>{ui}</ThemeProvider>);
}

function makeSummaryItem(metadata?: Record<string, unknown>): FlowItem {
  return {
    id: 'sum-1',
    type: FLOW_ITEM.TOOL_SUMMARY,
    content: 'Pods are CrashLooping in namespace prod.',
    status: TIMELINE_STATUS.COMPLETED,
    timestamp: '2026-08-21T10:00:00Z',
    sequenceNumber: 1,
    metadata,
  };
}

describe('summarizationModelLabel', () => {
  it('returns the model string', () => {
    expect(summarizationModelLabel({ summarization_model: 'gemini-flash' })).toBe('gemini-flash');
  });

  it('returns undefined when metadata is missing or blank', () => {
    expect(summarizationModelLabel(undefined)).toBeUndefined();
    expect(summarizationModelLabel({})).toBeUndefined();
    expect(summarizationModelLabel({ summarization_model: '  ' })).toBeUndefined();
  });
});

describe('SummarizationModelCaption', () => {
  it('renders the model name', () => {
    renderWithTheme(<SummarizationModelCaption metadata={{ summarization_model: 'gemini-flash' }} />);
    expect(screen.getByText('gemini-flash')).toBeInTheDocument();
  });

  it('renders nothing without a model', () => {
    const { container } = renderWithTheme(<SummarizationModelCaption metadata={{}} />);
    expect(container).toBeEmptyDOMElement();
  });
});

describe('ToolSummaryItem model caption', () => {
  it('shows the answering model next to TOOL RESULT SUMMARY', () => {
    renderWithTheme(
      <ToolSummaryItem item={makeSummaryItem({ summarization_model: 'claude-sonnet' })} />,
    );
    expect(screen.getByText('TOOL RESULT SUMMARY')).toBeInTheDocument();
    expect(screen.getByText('claude-sonnet')).toBeInTheDocument();
  });

  it('keeps the header unchanged when metadata has no model', () => {
    renderWithTheme(<ToolSummaryItem item={makeSummaryItem()} />);
    expect(screen.getByText('TOOL RESULT SUMMARY')).toBeInTheDocument();
    expect(screen.queryByText('gemini-flash')).not.toBeInTheDocument();
  });
});

describe('ToolCallItem session history model caption', () => {
  it('shows the answering model next to Summary', () => {
    const item: FlowItem = {
      id: 'search-1',
      type: FLOW_ITEM.TOOL_CALL,
      content: 'Digest of past nginx incidents.',
      status: TIMELINE_STATUS.COMPLETED,
      timestamp: '2026-08-21T10:00:00Z',
      sequenceNumber: 2,
      metadata: {
        tool_name: MEMORY_TOOL_NAME.SEARCH_PAST_SESSIONS,
        tool_type: TOOL_TYPE.MEMORY,
        arguments: { query: 'nginx' },
        summarization_model: 'claude-sonnet',
      },
    };

    renderWithTheme(<ToolCallItem item={item} expandAll />);
    expect(screen.getByText('Session History')).toBeInTheDocument();
    expect(screen.getByText('Summary')).toBeInTheDocument();
    expect(screen.getByText('claude-sonnet')).toBeInTheDocument();
  });
});
