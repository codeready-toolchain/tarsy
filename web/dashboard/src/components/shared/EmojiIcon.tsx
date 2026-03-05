import { Box } from '@mui/material';
import ContentPreviewTooltip from './ContentPreviewTooltip';
import { EMOJI_ICON_STYLES } from '../../constants/chatFlowAnimations';

interface EmojiIconProps {
  emoji: string;
  opacity: number;
  showTooltip?: boolean;
  tooltipContent?: string;
  tooltipType?: 'thought' | 'response' | 'final_answer' | 'forced_conclusion' | 'summarization';
  onClick?: () => void;
}

/**
 * EmojiIcon Component
 * Renders an emoji with optional tooltip for collapsed state
 */
export default function EmojiIcon({ 
  emoji, 
  opacity, 
  showTooltip = false,
  tooltipContent = '',
  tooltipType = 'thought',
  onClick,
}: EmojiIconProps) {
  const iconStyles = {
    ...EMOJI_ICON_STYLES,
    opacity,
    ...(showTooltip && { cursor: 'help' }),
    ...(onClick && !showTooltip && { cursor: 'pointer' }),
  };

  if (showTooltip) {
    return (
      <ContentPreviewTooltip content={tooltipContent} type={tooltipType}>
        <Box className="cfi-dimmable" sx={iconStyles} onClick={onClick}>
          {emoji}
        </Box>
      </ContentPreviewTooltip>
    );
  }

  return (
    <Box className="cfi-dimmable" sx={iconStyles} onClick={onClick}>
      {emoji}
    </Box>
  );
}
