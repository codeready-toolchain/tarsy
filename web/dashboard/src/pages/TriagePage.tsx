import { DashboardView } from '../components/dashboard/DashboardView.tsx';

/**
 * Triage page — grouped view of alerts needing attention, sharing DashboardView
 * with the Sessions page but rendering the triage groups instead of the session list.
 */
export function TriagePage() {
  return <DashboardView tab="triage" />;
}
