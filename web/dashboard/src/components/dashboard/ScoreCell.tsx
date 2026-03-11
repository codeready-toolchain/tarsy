import { TableCell } from '@mui/material';
import { useNavigate } from 'react-router-dom';
import { ScoreBadge } from '../common/ScoreBadge.tsx';
import { sessionScoringPath } from '../../constants/routes.ts';

interface ScoreCellProps {
  sessionId: string;
  score?: number | null;
  scoringStatus?: string | null;
}

export function ScoreCell({ sessionId, score, scoringStatus }: ScoreCellProps) {
  const navigate = useNavigate();
  const hasScoring = scoringStatus || score != null;

  return (
    <TableCell
      onClick={(e) => {
        if (hasScoring) {
          e.stopPropagation();
          navigate(sessionScoringPath(sessionId));
        }
      }}
      sx={hasScoring ? { cursor: 'pointer' } : undefined}
    >
      <ScoreBadge score={score} scoringStatus={scoringStatus} variant="pill" showLabel={false} />
    </TableCell>
  );
}
