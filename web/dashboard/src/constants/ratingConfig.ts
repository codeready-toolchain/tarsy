import { ThumbUp, ThumbsUpDown, ThumbDown } from '@mui/icons-material';
import { QUALITY_RATING } from '../types/api.ts';

export interface RatingConfigEntry {
  icon: typeof ThumbUp;
  color: 'success' | 'warning' | 'error';
  label: string;
}

export const RATING_CONFIG: Record<string, RatingConfigEntry> = {
  [QUALITY_RATING.ACCURATE]: { icon: ThumbUp, color: 'success', label: 'Accurate' },
  [QUALITY_RATING.PARTIALLY_ACCURATE]: { icon: ThumbsUpDown, color: 'warning', label: 'Partially Accurate' },
  [QUALITY_RATING.INACCURATE]: { icon: ThumbDown, color: 'error', label: 'Inaccurate' },
};
