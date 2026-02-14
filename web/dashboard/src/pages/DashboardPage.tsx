import { DashboardView } from '../components/dashboard/DashboardView.tsx';

/**
 * Dashboard page — main session list view with active alerts, filters, and history.
 *
 * DashboardView owns the full layout including AppBar, filters, panels,
 * and version footer — matching the old dashboard's DashboardView pattern.
 */
export function DashboardPage() {
  return <DashboardView />;
}
