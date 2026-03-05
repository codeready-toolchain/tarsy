import { useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';

interface TypewriterTextProps {
  text: string;
  speed?: number; // ms per character (default: 1)
  tickInterval?: number; // ms between state updates (default: 50)
  onComplete?: () => void;
  children?: (displayText: string, isAnimating: boolean) => ReactNode;
}

/**
 * Typewriter effect component for streaming content.
 *
 * Uses a throttled setInterval (default 150ms) instead of requestAnimationFrame
 * so downstream renderers (e.g. ReactMarkdown) are invoked ~7 times/sec
 * rather than 60, keeping CPU usage low on large growing content.
 *
 * Behavior:
 * - Growing text (e.g., "Hello" → "Hello World"): continues from current position
 * - Non-growing text (e.g., "Hello" → "Goodbye"): resets and starts fresh animation
 */
export default function TypewriterText({ 
  text, 
  speed = 1,
  tickInterval = 50,
  onComplete,
  children 
}: TypewriterTextProps) {
  const [displayedText, setDisplayedText] = useState('');
  const [isAnimating, setIsAnimating] = useState(false);
  
  const targetTextRef = useRef('');
  const displayedLengthRef = useRef(0);
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const lastUpdateTimeRef = useRef<number>(0);
  const completedRef = useRef(false);

  useEffect(() => {
    if (!text) {
      if (timerRef.current) {
        clearInterval(timerRef.current);
        timerRef.current = null;
      }
      setDisplayedText('');
      setIsAnimating(false);
      displayedLengthRef.current = 0;
      completedRef.current = true;
      targetTextRef.current = text;
      return;
    }
    
    const previousTarget = targetTextRef.current;
    targetTextRef.current = text;
    
    if (previousTarget === text) return;
    
    const isGrowing = text.startsWith(previousTarget);
    if (!isGrowing) {
      displayedLengthRef.current = 0;
      completedRef.current = false;
    }
    
    setIsAnimating(true);
    completedRef.current = false;
    lastUpdateTimeRef.current = performance.now();
    
    if (timerRef.current) return; // interval already running

    timerRef.current = setInterval(() => {
      const now = performance.now();
      const elapsed = now - lastUpdateTimeRef.current;
      const target = targetTextRef.current;
      const charsToAdd = Math.floor(elapsed / speed);
      
      if (charsToAdd > 0) {
        const newLength = Math.min(displayedLengthRef.current + charsToAdd, target.length);
        displayedLengthRef.current = newLength;
        setDisplayedText(target.slice(0, newLength));
        lastUpdateTimeRef.current = now;
        
        if (newLength >= target.length) {
          setIsAnimating(false);
          completedRef.current = true;
          if (timerRef.current) {
            clearInterval(timerRef.current);
            timerRef.current = null;
          }
          onComplete?.();
        }
      }
    }, tickInterval);
  }, [text, speed, tickInterval, onComplete]);
  
  useEffect(() => {
    return () => {
      if (timerRef.current) {
        clearInterval(timerRef.current);
        timerRef.current = null;
      }
    };
  }, []);
  
  if (children) return <>{children(displayedText, isAnimating)}</>;
  return <>{displayedText}</>;
}
