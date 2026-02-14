import { Box } from '@mui/material';
import ContentPreviewTooltip from './ContentPreviewTooltip';
import { EMOJI_ICON_STYLES } from '../../constants/chatFlowAnimations';

interface EmojiIconProps {
  emoji: string;
  opacity: number;
  showTooltip?: boolean;
  tooltipContent?: string;
  tooltipType?: 'thought' | 'native_thinking' | 'final_answer' | 'forced_conclusion' | 'summarization';
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
  tooltipType = 'thought'
}: EmojiIconProps) {
  const iconStyles = {
    ...EMOJI_ICON_STYLES,
    opacity,
    ...(showTooltip && { cursor: 'help' })
  };

  if (showTooltip) {
    return (
      <ContentPreviewTooltip content={tooltipContent} type={tooltipType}>
        <Box className="cfi-dimmable" sx={iconStyles}>
          {emoji}
        </Box>
      </ContentPreviewTooltip>
    );
  }

  return (
    <Box className="cfi-dimmable" sx={iconStyles}>
      {emoji}
    </Box>
  );
}
