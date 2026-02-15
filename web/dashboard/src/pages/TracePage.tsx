import { useParams } from 'react-router-dom';
import { Container, Typography } from '@mui/material';
import { SharedHeader } from '../components/layout/SharedHeader.tsx';

/**
 * Trace page — observability / debug view.
 * Placeholder shell — content implemented in Phase 7.5.
 */
export function TracePage() {
  const { id } = useParams<{ id: string }>();

  return (
    <>
      <Container maxWidth="lg" sx={{ py: 2, px: { xs: 1, sm: 2 } }}>
        <SharedHeader title="Trace View" showBackButton />
        <Typography variant="h5" sx={{ mt: 2, mb: 2 }}>
          Trace: {id}
        </Typography>
        <Typography color="text.secondary">
          Stage/execution hierarchy with interaction details will be implemented in Phase 7.5.
        </Typography>
      </Container>
    </>
  );
}
