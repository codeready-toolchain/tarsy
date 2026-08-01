import { createContext, useContext, useEffect, useState, type ReactNode } from 'react';

export interface PageHeaderConfig {
  title: string;
  /** Hierarchical back (session/trace/scoring). Not used for top-level peer pages — those rely on the drawer. */
  showBackButton?: boolean;
  /** Page-specific controls rendered in the header toolbar (toggles, badges, status). */
  actions?: ReactNode;
}

const DEFAULT_CONFIG: PageHeaderConfig = { title: 'TARSy' };

// Split into two contexts so pages that only *publish* (via usePageHeader) don't
// subscribe to the ever-changing config value — only the global header
// (usePageHeaderState) does. setConfig from useState never changes identity,
// so the setter context's value is stable and never triggers publisher re-renders.
// Combining these into one context previously caused an infinite render loop:
// publish -> config changes -> publishers re-render (subscribed) -> re-publish -> repeat.
const PageHeaderConfigContext = createContext<PageHeaderConfig | null>(null);
const PageHeaderSetterContext = createContext<((config: PageHeaderConfig) => void) | null>(null);

export function PageHeaderProvider({ children }: { children: ReactNode }) {
  const [config, setConfig] = useState<PageHeaderConfig>(DEFAULT_CONFIG);
  return (
    <PageHeaderSetterContext.Provider value={setConfig}>
      <PageHeaderConfigContext.Provider value={config}>{children}</PageHeaderConfigContext.Provider>
    </PageHeaderSetterContext.Provider>
  );
}

/**
 * Pages call this to publish their title/back-button/actions into the single
 * global AppBar owned by AppLayout. Re-publishes whenever the page re-renders
 * with different content, so live state (spinners, badges, toggles) stays in sync.
 */
// eslint-disable-next-line react-refresh/only-export-components -- hook colocated with its provider
export function usePageHeader(config: PageHeaderConfig): void {
  const setConfig = useContext(PageHeaderSetterContext);
  if (!setConfig) {
    throw new Error('usePageHeader must be used within PageHeaderProvider');
  }
  useEffect(() => {
    setConfig(config);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- config is a fresh object each render; re-publish is intentional so live header content stays current
  }, [setConfig, config.title, config.showBackButton, config.actions]);
}

/** Consumed by the global AppBar to render whatever the current page published. */
// eslint-disable-next-line react-refresh/only-export-components -- hook colocated with its provider
export function usePageHeaderState(): PageHeaderConfig {
  const config = useContext(PageHeaderConfigContext);
  if (config === null) {
    throw new Error('usePageHeaderState must be used within PageHeaderProvider');
  }
  return config;
}
