import { render, screen } from '@testing-library/react';
import { ThemeProvider, createTheme } from '@mui/material/styles';
import ForcedConclusionBadge from '../../components/timeline/ForcedConclusionBadge';
import { getFinalAnalysisPresentation } from '../../components/timeline/finalAnalysisPresentation';
import { STAGE_TYPE } from '../../constants/eventTypes';

const theme = createTheme();

describe('ForcedConclusionBadge', () => {
  it('renders the fallback string for inherited reason keys', () => {
    const { wrapUpBadge } = getFinalAnalysisPresentation(
      { reason: '__proto__' },
      STAGE_TYPE.INVESTIGATION,
      true,
    );
    expect(wrapUpBadge).toBe('forced conclusion');

    render(
      <ThemeProvider theme={theme}>
        <ForcedConclusionBadge label={wrapUpBadge!} />
      </ThemeProvider>,
    );

    expect(screen.getByText(/forced conclusion/i)).toBeInTheDocument();
  });
});
