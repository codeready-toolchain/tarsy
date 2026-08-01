import { Chip, Tooltip } from '@mui/material';
import { Wifi, WifiOff } from '@mui/icons-material';

interface ConnectionStatusChipProps {
  connected: boolean;
  onRetry?: () => void;
}

/**
 * WebSocket "Live"/"Offline" status chip — click to retry when disconnected.
 * Shared by ActiveAlertsPanel and TriageFilterBar so the two panels' connection
 * indicators stay visually and behaviorally identical.
 */
export function ConnectionStatusChip({ connected, onRetry }: ConnectionStatusChipProps) {
  return (
    <Tooltip
      title={
        connected
          ? 'Connected — real-time updates active'
          : 'Disconnected — click to retry connection'
      }
    >
      <Chip
        icon={connected ? <Wifi sx={{ fontSize: 16 }} /> : <WifiOff sx={{ fontSize: 16 }} />}
        label={connected ? 'Live' : 'Offline'}
        color={connected ? 'success' : 'default'}
        size="small"
        variant={connected ? 'filled' : 'outlined'}
        onClick={!connected ? onRetry : undefined}
      />
    </Tooltip>
  );
}
