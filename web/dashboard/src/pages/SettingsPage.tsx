/**
 * Review-notification settings. Reachable with or without login.
 */

import { useEffect, useState, type ReactNode } from 'react';
import CancelIcon from '@mui/icons-material/Cancel';
import CheckCircleIcon from '@mui/icons-material/CheckCircle';
import RestartAltIcon from '@mui/icons-material/RestartAlt';
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
import {
  defaultNotificationSettings,
  mergeLabelSuggestions,
  type NotificationSettings,
} from '../utils/notificationSettings.ts';
import {
  notificationPermission,
  requestNotificationPermission,
  type BrowserNotificationPermission,
} from '../utils/notificationPermission.ts';

function sameSettings(left: NotificationSettings, right: NotificationSettings): boolean {
  return left.master === right.master
    && left.tabCounter === right.tabCounter
    && left.desktop === right.desktop
    && left.labels.length === right.labels.length
    && left.labels.every((label, index) => label === right.labels[index]);
}

function PermissionNote({ icon, children }: { icon: ReactNode; children: string }) {
  return (
    <Stack direction="row" spacing={1} sx={{ alignItems: 'center', pl: 1.5 }}>
      {icon}
      <Typography variant="body2" color="text.secondary">{children}</Typography>
    </Stack>
  );
}

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
      <PermissionNote icon={<CancelIcon sx={{ fontSize: 16, color: 'error.main' }} />}>
        The browser is blocking toasts.
      </PermissionNote>
    );
  }
  if (permission === 'granted') {
    return (
      <PermissionNote icon={<CheckCircleIcon sx={{ fontSize: 16, color: 'success.main' }} />}>
        Desktop notifications are allowed.
      </PermissionNote>
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
  const { settings, updateSettings, resetSettings } = useNotificationSettings();
  const [permission, setPermission] = useState(notificationPermission);
  const [suggestions, setSuggestions] = useState<string[]>([]);

  useEffect(() => {
    const permissions = navigator.permissions;
    if (!permissions?.query) return;
    let status: globalThis.PermissionStatus | undefined;
    let cancelled = false;
    void permissions.query({ name: 'notifications' }).then((result) => {
      if (cancelled) return;
      status = result;
      result.onchange = () => {
        setPermission(notificationPermission());
      };
    }).catch(() => {
      // This browser does not expose notification permission changes.
    });
    return () => {
      cancelled = true;
      if (status) status.onchange = null;
    };
  }, []);

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
  const paused = !settings.master;

  return (
    <Box sx={{ backgroundColor: 'background.default' }}>
      <Container maxWidth={false} sx={{ py: 2, px: { xs: 1, sm: 2 } }}>
        <Stack spacing={2} sx={{ maxWidth: 480 }}>
          <Box>
            <Typography variant="h6" component="h2" sx={{ mb: 1 }}>
              Review notifications
            </Typography>
            <Typography variant="body2" color="text.secondary">
              Completed sessions that still need review.
              {paused && ' The header switch is off, so both signals are paused.'}
            </Typography>
          </Box>
          <FormControlLabel
            disabled={paused}
            control={(
              <Switch
                checked={!paused && settings.tabCounter}
                onChange={(_, checked) => updateSettings({ tabCounter: checked })}
              />
            )}
            label="Tab counter"
          />
          <FormControlLabel
            disabled={paused}
            control={(
              <Switch
                checked={!paused && settings.desktop}
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
          <Stack spacing={1} sx={{ alignItems: 'flex-start' }}>
            <PermissionStatus
              permission={permission}
              onAllow={() => {
                void requestNotificationPermission().then(setPermission);
              }}
            />
            <Button
              size="small"
              startIcon={<RestartAltIcon sx={{ fontSize: 16 }} />}
              onClick={resetSettings}
              disabled={sameSettings(settings, defaultNotificationSettings())}
              sx={{
                textTransform: 'none',
                minWidth: 0,
                p: (theme) => theme.spacing(0.5, 0, 0.5, 1.5),
                '& .MuiButton-startIcon': { ml: 0, mr: 1 },
                '& .MuiButton-startIcon > *:nth-of-type(1)': { fontSize: 16 },
              }}
            >
              Reset to defaults
            </Button>
          </Stack>
        </Stack>
        <VersionFooter />
      </Container>
      <FloatingSubmitAlertFab />
    </Box>
  );
}
