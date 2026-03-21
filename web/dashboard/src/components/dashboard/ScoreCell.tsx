import { TableCell, ButtonBase } from '@mui/material';
import type { SxProps, Theme } from '@mui/material/styles';
import { useNavigate } from 'react-router-dom';
import { ScoreBadge } from '../common/ScoreBadge.tsx';
import { sessionScoringPath } from '../../constants/routes.ts';

interface ScoreCellProps {
  sessionId: string;
  score?: number | null;
  scoringStatus?: string | null;
  sx?: SxProps<Theme>;
}

export function ScoreCell({ sessionId, score, scoringStatus, sx }: ScoreCellProps) {
  const navigate = useNavigate();
  const hasScoring = scoringStatus || score != null;

  return (
    <TableCell sx={sx}>
      {hasScoring ? (
        <ButtonBase
          onClick={(e) => {
            e.stopPropagation();
            navigate(sessionScoringPath(sessionId));
          }}
          aria-label="View scoring details"
          sx={{ cursor: 'pointer', borderRadius: 1 }}
        >
          <ScoreBadge score={score} scoringStatus={scoringStatus} variant="pill" showLabel={false} />
        </ButtonBase>
      ) : (
        <ScoreBadge score={score} scoringStatus={scoringStatus} variant="pill" showLabel={false} />
      )}
    </TableCell>
  );
}
