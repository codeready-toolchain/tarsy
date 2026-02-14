import { useState, useRef, useEffect } from 'react';
import { Button, Tooltip, IconButton } from '@mui/material';
import { ContentCopy, Check } from '@mui/icons-material';

interface CopyButtonProps {
  text: string;
  variant?: 'button' | 'icon';
  buttonVariant?: 'contained' | 'outlined' | 'text';
  size?: 'small' | 'medium' | 'large';
  label?: string;
  tooltip?: string;
}

/**
 * CopyButton component
 * Reusable copy-to-clipboard functionality with visual feedback
 */
function CopyButton({ 
  text, 
  variant = 'button',
  buttonVariant = 'outlined',
  size = 'small', 
  label = 'Copy',
  tooltip = 'Copy to clipboard'
}: CopyButtonProps) {
  const [copied, setCopied] = useState(false);
  const resetTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Cleanup timeout on unmount
  useEffect(() => {
    return () => {
      if (resetTimeoutRef.current) {
        clearTimeout(resetTimeoutRef.current);
      }
    };
  }, []);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      
      // Clear any existing timeout
      if (resetTimeoutRef.current) {
        clearTimeout(resetTimeoutRef.current);
      }
      
      // Reset after 2 seconds
      resetTimeoutRef.current = setTimeout(() => setCopied(false), 2000);
    } catch (err) {
      console.error('Failed to copy text:', err);
    }
  };

  if (variant === 'icon') {
    return (
      <Tooltip title={copied ? 'Copied!' : tooltip}>
        <IconButton
          size={size}
          onClick={handleCopy}
          color={copied ? 'success' : 'default'}
        >
          {copied ? <Check /> : <ContentCopy />}
        </IconButton>
      </Tooltip>
    );
  }

  return (
    <Tooltip title={copied ? 'Copied!' : tooltip}>
      <Button
        size={size}
        variant={buttonVariant}
        startIcon={copied ? <Check /> : <ContentCopy />}
        onClick={handleCopy}
        color={copied ? 'success' : 'primary'}
      >
        {copied ? 'Copied!' : label}
      </Button>
    </Tooltip>
  );
}

export default CopyButton;
