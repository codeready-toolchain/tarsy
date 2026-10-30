import NotificationsIcon from '@mui/icons-material/Notifications';
import NotificationsOffIcon from '@mui/icons-material/NotificationsOff';
import IconButton from '@mui/material/IconButton';
import Tooltip from '@mui/material/Tooltip';
import Box from '@mui/material/Box';
import { useNotificationSettings } from '../../contexts/NotificationSettingsContext.tsx';
import { themeToggleSx } from '../../theme/headerStyles.ts';
import { notificationPermission, requestNotificationPermission } from '../../utils/notificationPermission.ts';

/**
 * Header on/off control. It suspends or resumes the saved configuration
 * and does not edit the label field or the two signal switches.
 */
export function NotificationMasterSwitch() {
  const { settings, setMaster, backlogCount } = useNotificationSettings();

  const handleClick = () => {
    const turningOn = !settings.master;
    setMaster(turningOn);
    if (turningOn && settings.desktop && notificationPermission() === 'default') {
      void requestNotificationPermission();
    }
  };

  const label = settings.master
    ? `Review notifications on${backlogCount != null ? `, ${backlogCount} waiting` : ''}`
    : 'Review notifications off';

  return (
    <Tooltip title={settings.master ? 'Turn review notifications off' : 'Turn review notifications on'}>
      <IconButton
        id="review-notifications-switch"
        size="small"
        aria-pressed={settings.master}
        aria-label={label}
        onClick={handleClick}
        sx={{
          ml: 1,
          ...themeToggleSx,
          width: 'auto',
          minWidth: 40,
          px: backlogCount == null ? 0 : 1,
          gap: 0.5,
        }}
      >
        {settings.master
          ? <NotificationsIcon fontSize="small" />
          : <NotificationsOffIcon fontSize="small" />}
        {backlogCount != null && (
          <Box component="span" sx={{ fontSize: '0.75rem', fontWeight: 700, lineHeight: 1 }}>
            {backlogCount}
          </Box>
        )}
      </IconButton>
    </Tooltip>
  );
}
