import { useEffect } from 'react';
import { useNotificationSettings } from '../../contexts/NotificationSettingsContext.tsx';
import { notificationPermission, requestNotificationPermission } from '../../utils/notificationPermission.ts';

/**
 * Asks for desktop notification permission when the dashboard opens.
 * Skipped once the browser has been answered, or when that signal is off.
 */
export function NotificationPermissionPrompt() {
  const { settings } = useNotificationSettings();

  useEffect(() => {
    if (!settings.master || !settings.desktop) return;
    if (notificationPermission() !== 'default') return;
    void requestNotificationPermission();
  }, [settings.master, settings.desktop]);

  return null;
}
