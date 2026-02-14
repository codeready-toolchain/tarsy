import { useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';

interface TypewriterTextProps {
  text: string;
  speed?: number; // ms per character (default: 3)
  onComplete?: () => void;
  children?: (displayText: string, isAnimating: boolean) => ReactNode;
}

/**
 * Typewriter effect component for streaming content
 * 
 * Behavior:
 * - Growing text (e.g., "Hello" → "Hello World"): continues from current position
 * - Non-growing text (e.g., "Hello" → "Goodbye"): resets and starts fresh animation
 */
export default function TypewriterText({ 
  text, 
  speed = 3, 
  onComplete,
  children 
}: TypewriterTextProps) {
  const [displayedText, setDisplayedText] = useState('');
  const [isAnimating, setIsAnimating] = useState(false);
  
  const targetTextRef = useRef('');
  const displayedLengthRef = useRef(0);
  const animationFrameRef = useRef<number | null>(null);
  const lastUpdateTimeRef = useRef<number>(0);
  const completedRef = useRef(false);
  
  useEffect(() => {
    if (!text) {
      if (animationFrameRef.current) {
        cancelAnimationFrame(animationFrameRef.current);
        animationFrameRef.current = null;
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
    
    if (animationFrameRef.current) {
      cancelAnimationFrame(animationFrameRef.current);
      animationFrameRef.current = null;
    }
    
    const isGrowing = text.startsWith(previousTarget);
    if (!isGrowing) {
      displayedLengthRef.current = 0;
      completedRef.current = false;
    }
    
    setIsAnimating(true);
    completedRef.current = false;
    lastUpdateTimeRef.current = performance.now();
    
    const animate = (currentTime: number) => {
      const elapsed = currentTime - lastUpdateTimeRef.current;
      const target = targetTextRef.current;
      const charsToAdd = Math.floor(elapsed / speed);
      
      if (charsToAdd > 0) {
        const newLength = Math.min(displayedLengthRef.current + charsToAdd, target.length);
        displayedLengthRef.current = newLength;
        setDisplayedText(target.slice(0, newLength));
        lastUpdateTimeRef.current = currentTime;
        
        if (newLength >= target.length) {
          setIsAnimating(false);
          completedRef.current = true;
          animationFrameRef.current = null;
          onComplete?.();
          return;
        }
      }
      
      animationFrameRef.current = requestAnimationFrame(animate);
    };
    
    animationFrameRef.current = requestAnimationFrame(animate);
  }, [text, speed, onComplete]);
  
  useEffect(() => {
    return () => {
      if (animationFrameRef.current) {
        cancelAnimationFrame(animationFrameRef.current);
        animationFrameRef.current = null;
      }
    };
  }, []);
  
  if (children) return <>{children(displayedText, isAnimating)}</>;
  return <>{displayedText}</>;
}
