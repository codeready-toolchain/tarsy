/**
 * Review-notification settings. Reachable with or without login.
 */

import { useEffect, useState } from 'react';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import Container from '@mui/material/Container';
import FormControlLabel from '@mui/material/FormControlLabel';
import Stack from '@mui/material/Stack';
import Switch from '@mui/material/Switch';
import Typography from '@mui/material/Typography';
import { usePageHeader } from '../contexts/PageHeaderContext.tsx';
import { useNotificationSettings } from '../contexts/NotificationSettingsContext.tsx';
import { VersionFooter } from '../components/layout/VersionFooter.tsx';
import { FloatingSubmitAlertFab } from '../components/common/FloatingSubmitAlertFab.tsx';
import { NotificationLabelField } from '../components/settings/NotificationLabelField.tsx';
import { getFilterOptions, getSystemConfig } from '../services/api.ts';
import { mergeLabelSuggestions } from '../utils/notificationSettings.ts';
import {
  notificationPermission,
  requestNotificationPermission,
  type BrowserNotificationPermission,
} from '../utils/notificationPermission.ts';

function PermissionStatus({
  permission,
  onAllow,
}: {
  permission: BrowserNotificationPermission;
  onAllow: () => void;
}) {
  if (permission === 'unsupported') {
    return (
      <Typography variant="body2" color="text.secondary">
        Desktop toasts are not available in this browser. The tab counter still works.
      </Typography>
    );
  }
  if (permission === 'denied') {
    return (
      <Typography variant="body2" color="text.secondary">
        The browser is blocking toasts.
      </Typography>
    );
  }
  if (permission === 'granted') {
    return (
      <Typography variant="body2" color="text.secondary">
        Desktop notifications are allowed.
      </Typography>
    );
  }
  return (
    <Button variant="outlined" onClick={onAllow}>
      Allow notifications
    </Button>
  );
}

export function SettingsPage() {
  usePageHeader({ title: 'Settings' });
  const { settings, updateSettings } = useNotificationSettings();
  const [permission, setPermission] = useState(notificationPermission);
  const [suggestions, setSuggestions] = useState<string[]>([]);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      const [config, filters] = await Promise.all([
        getSystemConfig().catch(() => null),
        getFilterOptions().catch(() => null),
      ]);
      if (cancelled) return;
      const catalog: string[] = [];
      for (const map of Object.values(config?.label_maps ?? {})) {
        for (const spec of map.labels ?? []) {
          if (spec.label) catalog.push(spec.label);
        }
      }
      setSuggestions(mergeLabelSuggestions(catalog, filters?.labels ?? [], []));
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const options = mergeLabelSuggestions(suggestions, [], settings.labels);

  return (
    <Box sx={{ backgroundColor: 'background.default' }}>
      <Container maxWidth="md" sx={{ py: 2, px: { xs: 1, sm: 2 } }}>
        <Typography variant="h6" component="h2" sx={{ mb: 1 }}>
          Review notifications
        </Typography>
        <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
          Completed sessions that still need review. The header switch turns both signals on or off without changing this.
        </Typography>
        <Stack spacing={2} sx={{ maxWidth: 480 }}>
          <FormControlLabel
            control={(
              <Switch
                checked={settings.tabCounter}
                onChange={(_, checked) => updateSettings({ tabCounter: checked })}
              />
            )}
            label="Tab counter"
          />
          <FormControlLabel
            control={(
              <Switch
                checked={settings.desktop}
                onChange={(_, checked) => updateSettings({ desktop: checked })}
              />
            )}
            label="Desktop notifications"
          />
          <NotificationLabelField
            value={settings.labels}
            options={options}
            onChange={(labels) => updateSettings({ labels })}
          />
          <PermissionStatus
            permission={permission}
            onAllow={() => {
              void requestNotificationPermission().then(setPermission);
            }}
          />
        </Stack>
        <VersionFooter />
      </Container>
      <FloatingSubmitAlertFab />
    </Box>
  );
}
