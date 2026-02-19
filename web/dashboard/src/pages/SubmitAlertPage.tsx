/**
 * Submit Alert page — manual alert submission form.
 *
 * Layout:
 * - SharedHeader with back button and "Automated Incident Response" subtitle
 * - Backend health check on mount (error + success banners)
 * - ManualAlertForm (the core form component) with Fade transition
 * - VersionFooter inside content container
 */

import { useState, useEffect } from 'react';
import { Box, Container, Typography, Alert as MuiAlert, Fade } from '@mui/material';

import { SharedHeader } from '../components/layout/SharedHeader.tsx';
import { VersionFooter } from '../components/layout/VersionFooter.tsx';
import { ManualAlertForm } from '../components/alert/ManualAlertForm.tsx';
import { getHealth } from '../services/api.ts';

export function SubmitAlertPage() {
  const [backendStatus, setBackendStatus] = useState<'unknown' | 'healthy' | 'error'>('unknown');

  // Backend health check on mount
  useEffect(() => {
    const checkHealth = async () => {
      try {
        const resp = await getHealth();
        if (resp.status === 'healthy') {
          setBackendStatus('healthy');
        } else {
          setBackendStatus('error');
        }
      } catch (error) {
        setBackendStatus('error');
        console.error('Backend health check failed:', error);
      }
    };

    checkHealth();
  }, []);

  return (
    <Box sx={{ minHeight: '100vh', backgroundColor: 'background.default', px: 2, py: 2 }}>
      <SharedHeader title="Manual Alert Submission" showBackButton>
        <Typography variant="body2" sx={{ opacity: 0.8, color: 'white', mr: 2 }}>
          Automated Incident Response
        </Typography>
      </SharedHeader>

      <Container maxWidth={false} sx={{ py: 4, px: { xs: 1, sm: 2 } }}>
        {/* Backend status indicator */}
        {backendStatus === 'error' && (
          <MuiAlert severity="error" sx={{ mb: 3 }}>
            <Typography variant="body2">
              <strong>Backend Unavailable:</strong> The TARSy backend is not responding.
              Please ensure the backend server is running.
            </Typography>
          </MuiAlert>
        )}

        {backendStatus === 'healthy' && (
          <MuiAlert severity="success" sx={{ mb: 3 }}>
            <Typography variant="body2">
              TARSy is ready! Submit an alert to see automated incident analysis in action.
            </Typography>
          </MuiAlert>
        )}

        {/* Main content - Form navigates directly to session detail on submission */}
        <Fade in timeout={500}>
          <Box>
            <ManualAlertForm />
          </Box>
        </Fade>

        {/* Version footer */}
        <VersionFooter />
      </Container>
    </Box>
  );
}
