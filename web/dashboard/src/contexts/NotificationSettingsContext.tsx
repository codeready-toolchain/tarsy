import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';
import {
  defaultNotificationSettings,
  loadNotificationSettings,
  NOTIFICATION_SETTINGS_KEY,
  saveNotificationSettings,
  type NotificationSettings,
} from '../utils/notificationSettings.ts';

interface NotificationSettingsContextValue {
  settings: NotificationSettings;
  setMaster: (on: boolean) => void;
  updateSettings: (patch: Partial<Omit<NotificationSettings, 'master'>>) => void;
  resetSettings: () => void;
  backlogCount: number | null;
  setBacklogCount: (count: number | null) => void;
}

const NotificationSettingsContext = createContext<NotificationSettingsContextValue | null>(null);

export function NotificationSettingsProvider({ children }: { children: ReactNode }) {
  const [settings, setSettings] = useState(loadNotificationSettings);
  const [backlogCount, setBacklogCount] = useState<number | null>(null);

  const setMaster = useCallback((master: boolean) => {
    setSettings((prev) => {
      const next = { ...prev, master };
      saveNotificationSettings(next);
      return next;
    });
    if (!master) setBacklogCount(null);
  }, []);

  const updateSettings = useCallback((patch: Partial<Omit<NotificationSettings, 'master'>>) => {
    setSettings((prev) => {
      const next = { ...prev, ...patch };
      saveNotificationSettings(next);
      return next;
    });
  }, []);

  const resetSettings = useCallback(() => {
    const next = defaultNotificationSettings();
    saveNotificationSettings(next);
    setSettings(next);
  }, []);

  useEffect(() => {
    const onStorage = (event: StorageEvent) => {
      if (event.key !== NOTIFICATION_SETTINGS_KEY) return;
      setSettings(loadNotificationSettings());
    };
    window.addEventListener('storage', onStorage);
    return () => window.removeEventListener('storage', onStorage);
  }, []);

  const value = useMemo(
    () => ({ settings, setMaster, updateSettings, resetSettings, backlogCount, setBacklogCount }),
    [settings, setMaster, updateSettings, resetSettings, backlogCount],
  );

  return (
    <NotificationSettingsContext.Provider value={value}>
      {children}
    </NotificationSettingsContext.Provider>
  );
}

// eslint-disable-next-line react-refresh/only-export-components -- hook colocated with its provider
export function useNotificationSettings(): NotificationSettingsContextValue {
  const ctx = useContext(NotificationSettingsContext);
  if (!ctx) {
    throw new Error('useNotificationSettings must be used within NotificationSettingsProvider');
  }
  return ctx;
}
