import { Link } from 'react-router-dom';
import { Container, Typography, Button, Box } from '@mui/material';
import { usePageHeader } from '../contexts/PageHeaderContext.tsx';

/**
 * 404 Not Found page.
 */
export function NotFoundPage() {
  usePageHeader({ title: 'TARSy' });

  return (
    <>
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
          <Typography variant="h3" gutterBottom sx={{
            color: 'text.secondary'
          }}>
            404
          </Typography>
          <Typography variant="h5" gutterBottom>
            Page Not Found
          </Typography>
          <Typography
            sx={{
              color: 'text.secondary',
              mb: 3
            }}>
            The page you are looking for does not exist.
          </Typography>
          <Button component={Link} nativeButton={false} to="/" variant="contained">
            Go to Alert Investigations
          </Button>
        </Box>
      </Container>
    </>
  );
}
