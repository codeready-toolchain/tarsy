import { ThumbUp, ThumbsUpDown, ThumbDown } from '@mui/icons-material';
import { QUALITY_RATING } from '../types/api.ts';
import type { QualityRating } from '../types/api.ts';

export interface RatingConfigEntry {
  icon: typeof ThumbUp;
  color: 'success' | 'warning' | 'error';
  label: string;
}

/** Keyed by the QualityRating union so the compiler enforces every rating has an entry. */
export const RATING_CONFIG: Record<QualityRating, RatingConfigEntry> = {
  [QUALITY_RATING.ACCURATE]: { icon: ThumbUp, color: 'success', label: 'Accurate' },
  [QUALITY_RATING.PARTIALLY_ACCURATE]: { icon: ThumbsUpDown, color: 'warning', label: 'Partially Accurate' },
  [QUALITY_RATING.INACCURATE]: { icon: ThumbDown, color: 'error', label: 'Inaccurate' },
};

/** Safe lookup for runtime strings that may not be a valid QualityRating. */
export function getRatingConfig(rating: string | null | undefined): RatingConfigEntry | undefined {
  if (!rating) return undefined;
  return (RATING_CONFIG as Record<string, RatingConfigEntry>)[rating];
}
