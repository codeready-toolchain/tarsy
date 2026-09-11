import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ThemeProvider, createTheme } from '@mui/material/styles';
import { FormControl, InputLabel, MenuItem, Select } from '@mui/material';
import { theme } from '../theme';

async function openScoringSelect() {
  const user = userEvent.setup();
  await user.click(screen.getByRole('combobox', { name: 'Scoring' }));
  expect(screen.getByRole('listbox')).toBeInTheDocument();
}

function ScoringSelect() {
  return (
    <FormControl>
      <InputLabel id="scoring-label">Scoring</InputLabel>
      <Select labelId="scoring-label" label="Scoring" defaultValue="">
        <MenuItem value="scored">Scored</MenuItem>
      </Select>
    </FormControl>
  );
}

describe('theme overlay scroll lock', () => {
  afterEach(() => {
    document.body.style.overflow = '';
    document.documentElement.style.overflow = '';
  });

  it('keeps body overflow when a Select menu opens', async () => {
    render(
      <ThemeProvider theme={theme}>
        <ScoringSelect />
      </ThemeProvider>,
    );

    await openScoringSelect();
    expect(document.body).not.toHaveStyle({ overflow: 'hidden' });
  });

  it('MUI default Select still locks body overflow', async () => {
    render(
      <ThemeProvider theme={createTheme()}>
        <ScoringSelect />
      </ThemeProvider>,
    );

    await openScoringSelect();
    expect(document.body).toHaveStyle({ overflow: 'hidden' });
  });
});
