import { Container, Typography } from '@mui/material';
import { SharedHeader } from '../components/layout/SharedHeader.tsx';

/**
 * Submit alert page — manual alert submission form.
 * Placeholder shell — content implemented in Phase 7.6.
 */
export function SubmitAlertPage() {
  return (
    <>
      <SharedHeader title="Submit Alert" showBack />
      <Container maxWidth="md">
        <Typography variant="h5" sx={{ mt: 2, mb: 2 }}>
          Submit Alert
        </Typography>
        <Typography color="text.secondary">
          Manual alert submission form will be implemented in Phase 7.6.
        </Typography>
      </Container>
    </>
  );
}
