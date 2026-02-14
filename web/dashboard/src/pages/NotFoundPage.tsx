import { Link } from 'react-router-dom';
import { Container, Typography, Button, Box } from '@mui/material';
import { SharedHeader } from '../components/layout/SharedHeader.tsx';

/**
 * 404 Not Found page.
 */
export function NotFoundPage() {
  return (
    <>
      <SharedHeader title="TARSy Dashboard" />
      <Container maxWidth="sm">
        <Box
          sx={{
            mt: 8,
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            textAlign: 'center',
          }}
        >
          <Typography variant="h3" color="text.secondary" gutterBottom>
            404
          </Typography>
          <Typography variant="h5" gutterBottom>
            Page Not Found
          </Typography>
          <Typography color="text.secondary" sx={{ mb: 3 }}>
            The page you are looking for does not exist.
          </Typography>
          <Button component={Link} to="/" variant="contained">
            Go to Dashboard
          </Button>
        </Box>
      </Container>
    </>
  );
}
