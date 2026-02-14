import { useParams } from 'react-router-dom';
import { Container, Typography } from '@mui/material';
import { SharedHeader } from '../components/layout/SharedHeader.tsx';

/**
 * Session detail page — conversation timeline view.
 * Placeholder shell — content implemented in Phase 7.3.
 */
export function SessionDetailPage() {
  const { id } = useParams<{ id: string }>();

  return (
    <>
      <SharedHeader title="Session Detail" showBack />
      <Container maxWidth="lg">
        <Typography variant="h5" sx={{ mt: 2, mb: 2 }}>
          Session: {id}
        </Typography>
        <Typography color="text.secondary">
          Conversation timeline, streaming, and stage progress will be implemented in Phase 7.3.
        </Typography>
      </Container>
    </>
  );
}
