/**
 * Version context — shares version monitoring state across components.
 *
 * Wraps useVersionMonitor() in a context to prevent duplicate polling.
 * Ported from old TARSy dashboard, adapted for React 19 context pattern.
 */

import { createContext, useContext, type ReactNode } from 'react';
import { useVersionMonitor, type VersionInfo } from '../hooks/useVersionMonitor.ts';

const VersionContext = createContext<VersionInfo | undefined>(undefined);

/** Provides version monitoring state to the component tree. */
export function VersionProvider({ children }: { children: ReactNode }) {
  const versionInfo = useVersionMonitor();
  return <VersionContext value={versionInfo}>{children}</VersionContext>;
}

/** Access version monitoring information. Must be used within a VersionProvider. */
// eslint-disable-next-line react-refresh/only-export-components
export function useVersion(): VersionInfo {
  const context = useContext(VersionContext);
  if (context === undefined) {
    throw new Error('useVersion must be used within a VersionProvider');
  }
  return context;
}
