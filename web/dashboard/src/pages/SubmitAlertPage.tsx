/**
 * Submit Alert page — manual alert submission form.
 *
 * Layout mirrors old TARSy ManualAlertSubmission.tsx:
 * - SharedHeader with back button
 * - Backend health check on mount (status banner if unhealthy)
 * - ManualAlertForm (the core form component)
 * - VersionFooter
 */

import { useState, useEffect } from 'react';
import { Container, Alert as MuiAlert, Box } from '@mui/material';

import { SharedHeader } from '../components/layout/SharedHeader.tsx';
import { VersionFooter } from '../components/layout/VersionFooter.tsx';
import { ManualAlertForm } from '../components/alert/ManualAlertForm.tsx';
import { getHealth } from '../services/api.ts';

export function SubmitAlertPage() {
  const [healthError, setHealthError] = useState<string | null>(null);

  // Backend health check on mount
  useEffect(() => {
    const checkHealth = async () => {
      try {
        const resp = await getHealth();
        if (resp.status !== 'ok') {
          setHealthError('Backend reported degraded status. Submissions may fail.');
        }
      } catch {
        setHealthError(
          'Unable to reach backend. Please verify the backend is running and try again.',
        );
      }
    };

    checkHealth();
  }, []);

  return (
    <Box sx={{ minHeight: '100vh', display: 'flex', flexDirection: 'column' }}>
      <SharedHeader title="Submit Alert" showBackButton />

      <Container maxWidth="md" sx={{ flex: 1, py: 2 }}>
        {/* Health check warning */}
        {healthError && (
          <MuiAlert
            severity="warning"
            sx={{ mb: 3, borderRadius: 3 }}
            onClose={() => setHealthError(null)}
          >
            {healthError}
          </MuiAlert>
        )}

        <ManualAlertForm />
      </Container>

      <VersionFooter />
    </Box>
  );
}
