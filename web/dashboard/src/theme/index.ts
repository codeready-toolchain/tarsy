import { createTheme } from '@mui/material/styles';

/**
 * MUI theme.
 * Uses MUI's alpha() utility for lighter/darker variants.
 */
export const theme = createTheme({
  palette: {
    mode: 'light',
    primary: {
      main: '#1976d2', // Material Blue
    },
    secondary: {
      main: '#424242', // Material Grey
    },
    success: {
      main: '#2e7d32', // Green for completed alerts
    },
    error: {
      main: '#d32f2f', // Red for failed alerts
    },
    warning: {
      main: '#ed6c02', // Orange for pending alerts
    },
    info: {
      main: '#0288d1', // Light blue for processing alerts
    },
    background: {
      default: '#fafafa',
      paper: '#ffffff',
    },
  },
  typography: {
    fontFamily: 'Roboto, Arial, sans-serif',
    h6: {
      fontWeight: 600, // AppBar title
    },
    h5: {
      fontWeight: 500,
    },
  },
  components: {
    MuiChip: {
      styleOverrides: {
        root: {
          fontWeight: 500,
        },
      },
    },
    MuiTableCell: {
      styleOverrides: {
        head: {
          fontWeight: 600,
          backgroundColor: '#f5f5f5',
        },
      },
    },
    MuiPaper: {
      styleOverrides: {
        root: {
          boxShadow: '0 1px 3px rgba(0,0,0,0.12), 0 1px 2px rgba(0,0,0,0.24)',
        },
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
