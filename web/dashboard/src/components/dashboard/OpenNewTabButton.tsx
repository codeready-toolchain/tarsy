import { IconButton, Tooltip } from '@mui/material';
import { OpenInNew } from '@mui/icons-material';
import { sessionDetailPath } from '../../constants/routes.ts';

interface OpenNewTabButtonProps {
  sessionId: string;
}

export function OpenNewTabButton({ sessionId }: OpenNewTabButtonProps) {
  const handleClick = (e: React.MouseEvent) => {
    e.stopPropagation();
    window.open(
      `${window.location.origin}${sessionDetailPath(sessionId)}`,
      '_blank',
      'noopener,noreferrer',
    );
  };

  return (
    <Tooltip title="Open in new tab">
      <IconButton
        size="small"
        onClick={handleClick}
        sx={{ opacity: 0.5, '&:hover': { opacity: 1 } }}
      >
        <OpenInNew sx={{ fontSize: 16 }} />
      </IconButton>
    </Tooltip>
  );
}
