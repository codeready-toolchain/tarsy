import { render, screen } from '@testing-library/react';
import type { ReactElement } from 'react';
import { ThemeProvider, createTheme } from '@mui/material/styles';
import LLMInteractionPreview from '../../components/trace/LLMInteractionPreview';
import type { LLMInteractionListItem } from '../../types/trace';

const theme = createTheme();

function renderWithTheme(ui: ReactElement) {
  return render(<ThemeProvider theme={theme}>{ui}</ThemeProvider>);
}

function makeItem(overrides?: Partial<LLMInteractionListItem>): LLMInteractionListItem {
  return {
    id: 'llm-1',
    interaction_type: 'iteration',
    model_name: 'gemini-2.0-flash',
    input_tokens: 100,
    output_tokens: 30,
    total_tokens: 130,
    created_at: '2026-08-26T10:00:00Z',
    ...overrides,
  };
}

describe('LLMInteractionPreview', () => {
  it('shows cache tokens when present', () => {
    renderWithTheme(
      <LLMInteractionPreview
        interaction={makeItem({ cache_read_tokens: 40, cache_creation_tokens: 10 })}
      />,
    );

    expect(screen.getByText('cache read')).toBeInTheDocument();
    expect(screen.getByText('cache create')).toBeInTheDocument();
    expect(screen.getByText('40')).toBeInTheDocument();
    expect(screen.getByText('10')).toBeInTheDocument();
  });

  it('hides cache tokens when fields are absent', () => {
    renderWithTheme(<LLMInteractionPreview interaction={makeItem()} />);

    expect(screen.getByText('100')).toBeInTheDocument();
    expect(screen.queryByText('cache read')).not.toBeInTheDocument();
    expect(screen.queryByText('cache create')).not.toBeInTheDocument();
  });
});
