import { useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';

interface TypewriterTextProps {
  text: string;
  speed?: number; // ms per character at base rate (default: 3)
  tickInterval?: number; // ms between state updates (default: 16 ≈ 60fps)
  onComplete?: () => void;
  children?: (displayText: string, isAnimating: boolean) => ReactNode;
}

const SMOOTH_BUFFER = 20;
const CATCHUP_RATE = 0.3;

/**
 * Adaptive typewriter for streaming content.
 *
 * Reveal speed scales with the "buffer" — how far the display lags behind the
 * target text.
 *
 *   buffer ≤ 20 chars  →  base speed (3ms/char ≈ 5 chars/frame) — smooth flow
 *   buffer > 20 chars  →  base + 30% of excess per frame — progressive catch-up
 *
 * This gives a smooth typewriter when the LLM is slow, and automatic
 * acceleration when it's fast or delivers a burst. The catch-up decays
 * exponentially (buffer * 0.7^n), so large bursts resolve in ~160ms and
 * the reveal naturally eases back to the smooth base rate.
 *
 * Tick cost is <0.1ms even at 28K+ chars (profiled), so the 60fps cadence
 * is safe with 15-20 concurrent instances.
 */
export default function TypewriterText({ 
  text, 
  speed: rawSpeed = 3,
  tickInterval: rawTickInterval = 16,
  onComplete,
  children 
}: TypewriterTextProps) {
  const speed = Math.max(1, rawSpeed);
  const tickInterval = Math.max(1, rawTickInterval);
  const [displayedText, setDisplayedText] = useState('');
  const [isAnimating, setIsAnimating] = useState(false);
  
  const targetTextRef = useRef('');
  const displayedLengthRef = useRef(0);
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const lastUpdateTimeRef = useRef<number>(0);
  const completedRef = useRef(false);
  const onCompleteRef = useRef(onComplete);

  useEffect(() => {
    onCompleteRef.current = onComplete;
  }, [onComplete]);

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
    
    if (timerRef.current) return;

    timerRef.current = setInterval(() => {
      const now = performance.now();
      const elapsed = now - lastUpdateTimeRef.current;
      const target = targetTextRef.current;

      const baseChars = Math.floor(elapsed / speed);
      if (baseChars <= 0) return;

      const buffer = target.length - displayedLengthRef.current;
      let charsToAdd = baseChars;
      if (buffer > SMOOTH_BUFFER) {
        charsToAdd += Math.ceil((buffer - SMOOTH_BUFFER) * CATCHUP_RATE);
      }

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
        onCompleteRef.current?.();
      }
    }, tickInterval);
  }, [text, speed, tickInterval]);
  
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
