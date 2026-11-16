import type { Theme } from '@mui/material/styles';

/**
 * Action-stage wrench chip (also the post-investigation chip): green when the agent acted, light grey outline when it did not.
 * Shared by HistoricalAlertsList (SessionListItem) and Triage (TriageSessionRow).
 */
export function actionStageChipStyles(theme: Theme, actionsExecuted: boolean) {
  const green = theme.palette.success.main;
  return actionsExecuted
    ? {
        borderColor: green,
        color: green,
        '& .MuiChip-icon': { mx: 0, color: green },
      }
    : {
        borderColor: theme.palette.grey[400],
        color: theme.palette.grey[400],
        '& .MuiChip-icon': { mx: 0, color: theme.palette.grey[400] },
      };
}
