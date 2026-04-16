/**
 * Tooltip Component (React)
 * Shows additional information on hover with delay support.
 * CSS for .tooltip-wrapper and .tooltip-bubble is defined in global.css.
 */
import { useState, useRef, useEffect, type ReactNode } from 'react';
import { cn } from '@/lib/cn';

interface TooltipProps {
  content: string;
  position?: 'top' | 'bottom' | 'left' | 'right';
  delay?: number;
  className?: string;
  children: ReactNode;
}

export function Tooltip({
  content,
  position = 'top',
  delay = 200,
  className,
  children,
}: TooltipProps) {
  const [visible, setVisible] = useState(false);
  const timeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  function show() {
    timeoutRef.current = setTimeout(() => {
      setVisible(true);
    }, delay);
  }

  function hide() {
    if (timeoutRef.current) {
      clearTimeout(timeoutRef.current);
      timeoutRef.current = null;
    }
    setVisible(false);
  }

  useEffect(() => {
    return () => {
      if (timeoutRef.current) {
        clearTimeout(timeoutRef.current);
      }
    };
  }, []);

  return (
    <span
      className={cn('tooltip-wrapper', className)}
      data-tooltip-position={position}
      onMouseEnter={show}
      onMouseLeave={hide}
      onFocus={show}
      onBlur={hide}
    >
      {children}
      <span
        className={cn('tooltip-bubble', visible && 'visible')}
        role="tooltip"
      >
        {content}
      </span>
    </span>
  );
}

export default Tooltip;
