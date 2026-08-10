import { useState, useRef, useEffect } from 'react';
import { Tooltip, IconButton } from '@mui/material';
import { Link, Check } from '@mui/icons-material';

interface CopyLinkButtonProps {
  /** Absolute URL to copy */
  url: string;
  size?: 'small' | 'medium' | 'large';
  tooltip?: string;
}

/**
 * Copy a shareable deep link (distinct from content CopyButton).
 */
function CopyLinkButton({
  url,
  size = 'small',
  tooltip = 'Copy link',
}: CopyLinkButtonProps) {
  const [copied, setCopied] = useState(false);
  const resetTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    return () => {
      if (resetTimeoutRef.current) {
        clearTimeout(resetTimeoutRef.current);
      }
    };
  }, []);

  const handleCopy = async (e: React.MouseEvent) => {
    e.stopPropagation();
    e.preventDefault();
    try {
      await navigator.clipboard.writeText(url);
      setCopied(true);
      if (resetTimeoutRef.current) {
        clearTimeout(resetTimeoutRef.current);
      }
      resetTimeoutRef.current = setTimeout(() => setCopied(false), 2000);
    } catch (err) {
      console.error('Failed to copy link:', err);
    }
  };

  return (
    <Tooltip title={copied ? 'Copied!' : tooltip}>
      <IconButton
        size={size}
        onClick={handleCopy}
        color={copied ? 'success' : 'default'}
        aria-label={tooltip}
      >
        {copied ? <Check fontSize="inherit" /> : <Link fontSize="inherit" />}
      </IconButton>
    </Tooltip>
  );
}

export default CopyLinkButton;
