import type { SxProps, Theme } from '@mui/material/styles';

/**
 * Shared layout for Eval Score + Review columns (Alert History table).
 * Score column must stay content-sized — otherwise the table distributes extra width
 * into that cell and the badge sits far from the Review chip.
 */

export const qualityEvalScoreHeaderSx: SxProps<Theme> = {
  borderLeft: '1px solid',
  borderLeftColor: (theme) => theme.palette.divider,
  py: 1,
  px: 1,
  width: '1%',
  whiteSpace: 'nowrap',
  textAlign: 'center',
  verticalAlign: 'bottom',
};

export const qualityReviewHeaderSx: SxProps<Theme> = {
  borderRight: '1px solid',
  borderRightColor: (theme) => theme.palette.divider,
  py: 1,
  px: 1,
  width: 40,
  textAlign: 'center',
  verticalAlign: 'bottom',
};

export const qualityEvalScoreBodySx: SxProps<Theme> = {
  py: 1,
  px: 1,
  width: '1%',
  whiteSpace: 'nowrap',
  textAlign: 'center',
  verticalAlign: 'middle',
};

export const qualityReviewBodySx: SxProps<Theme> = {
  py: 1,
  px: 1,
  width: 40,
  textAlign: 'center',
  verticalAlign: 'middle',
};
