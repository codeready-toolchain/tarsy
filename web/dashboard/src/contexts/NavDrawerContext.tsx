import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from 'react';

export const NAV_DRAWER_WIDTH = 260;
export const NAV_DRAWER_WIDTH_COLLAPSED = 64;

const STORAGE_KEY = 'tarsy-nav-drawer-open';

interface NavDrawerContextValue {
  open: boolean;
  toggle: () => void;
  setOpen: (open: boolean) => void;
}

const NavDrawerContext = createContext<NavDrawerContextValue | null>(null);

function readStoredOpen(): boolean {
  try {
    return localStorage.getItem(STORAGE_KEY) === 'true';
  } catch {
    return false;
  }
}

function writeStoredOpen(open: boolean): void {
  try {
    localStorage.setItem(STORAGE_KEY, String(open));
  } catch {
    // Ignore quota / private-mode failures.
  }
}

export function NavDrawerProvider({ children }: { children: ReactNode }) {
  const [open, setOpenState] = useState(readStoredOpen);

  const setOpen = useCallback((next: boolean) => {
    setOpenState(next);
    writeStoredOpen(next);
  }, []);

  const toggle = useCallback(() => {
    setOpenState((prev) => {
      const next = !prev;
      writeStoredOpen(next);
      return next;
    });
  }, []);

  const value = useMemo(() => ({ open, toggle, setOpen }), [open, toggle, setOpen]);

  return <NavDrawerContext.Provider value={value}>{children}</NavDrawerContext.Provider>;
}

// eslint-disable-next-line react-refresh/only-export-components -- hook colocated with its provider
export function useNavDrawer(): NavDrawerContextValue {
  const ctx = useContext(NavDrawerContext);
  if (!ctx) {
    throw new Error('useNavDrawer must be used within NavDrawerProvider');
  }
  return ctx;
}
