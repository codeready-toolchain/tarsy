import { createTheme } from '@mui/material/styles';

/**
 * Single static MUI v7 theme with CSS variables and dual color schemes.
 *
 * Mode switches update CSS custom properties on <html data-theme="dark|light">,
 * not the React tree. Use `useColorScheme()` to read/set mode, and
 * `theme.applyStyles('dark', {...})` for mode-specific CSS in sx/styled.
 */
export const theme = createTheme({
  cssVariables: {
    colorSchemeSelector: 'data-theme',
  },
  colorSchemes: {
    light: {
      palette: {
        primary: { main: '#3949AB' },
        secondary: { main: '#546E7A' },
        success: { main: '#00796B' },
        error: { main: '#C62828' },
        warning: { main: '#F9A825' },
        info: { main: '#0288d1' },
        background: { default: '#F1F3F9', paper: '#FAFBFF' },
      },
    },
    dark: {
      palette: {
        primary: { main: '#7986CB' },
        secondary: { main: '#90A4AE' },
        success: { main: '#4DB6AC' },
        error: { main: '#EF9A9A' },
        warning: { main: '#FFD54F' },
        info: { main: '#81D4FA' },
        background: { default: '#0F1320', paper: '#181D2E' },
      },
    },
  },
  typography: {
    fontFamily: 'Roboto, Arial, sans-serif',
    h6: { fontWeight: 600 },
    h5: { fontWeight: 500 },
  },
  components: {
    MuiCssBaseline: {
      styleOverrides: (theme) => ({
        html: {
          scrollbarGutter: 'stable',
        },
        body: {
          ...theme.applyStyles('dark', {
            scrollbarColor: '#3949AB #181D2E',
          }),
        },
        '.search-highlight': {
          background: '#fff59d',
          color: '#000',
          padding: '0 1px',
          borderRadius: '2px',
          ...theme.applyStyles('dark', {
            background: '#f9a825',
            color: '#000',
          }),
        },
      }),
    },
    MuiChip: {
      styleOverrides: {
        root: { fontWeight: 500 },
      },
    },
    MuiTableCell: {
      styleOverrides: {
        head: ({ theme }) => ({
          fontWeight: 600,
          backgroundColor: '#E8EBF5',
          ...theme.applyStyles('dark', {
            backgroundColor: 'rgba(121, 134, 203, 0.08)',
          }),
        }),
      },
    },
    MuiContainer: {
      styleOverrides: {
        root: {
          paddingTop: '16px',
          paddingBottom: '16px',
        },
      },
    },
  },
});
