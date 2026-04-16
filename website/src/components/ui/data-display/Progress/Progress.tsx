import { type HTMLAttributes, type Ref, type ReactNode } from 'react';
import { cn } from '@/lib/cn';
import { progressTrackVariants, progressBarVariants, type ProgressVariants } from './progress.variants';

interface ProgressProps extends Omit<HTMLAttributes<HTMLDivElement>, 'ref'> {
  ref?: Ref<HTMLDivElement>;
  value?: number;
  max?: number;
  variant?: ProgressVariants['variant'];
  size?: ProgressVariants['size'];
  showLabel?: boolean;
  children?: ReactNode;
}

export function Progress({ ref, value, max = 100, variant = 'default', size = 'md', showLabel = false, className, children, ...rest }: ProgressProps) {
  const isIndeterminate = value === undefined;
  const percentage = isIndeterminate || max <= 0 ? 0 : Math.min(100, Math.max(0, (value / max) * 100));

  return (
    <div ref={ref} className={cn('w-full', className)} {...rest}>
      {showLabel && !isIndeterminate && (
        <div className="flex justify-between mb-1.5">
          {children}
          <span className="text-xs font-medium text-foreground-muted">{Math.round(percentage)}%</span>
        </div>
      )}
      <div
        className={cn(progressTrackVariants({ size }))}
        role="progressbar"
        aria-valuenow={isIndeterminate ? undefined : Math.round(percentage)}
        aria-valuemin={0}
        aria-valuemax={max}
      >
        <div
          className={cn(progressBarVariants({ variant, indeterminate: isIndeterminate }))}
          style={!isIndeterminate ? { width: `${percentage}%` } : undefined}
        />
      </div>
    </div>
  );
}

export default Progress;
