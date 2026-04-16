import { type HTMLAttributes, type Ref } from 'react';
import { cn } from '@/lib/cn';
import { skeletonVariants, type SkeletonVariants } from './skeleton.variants';

interface SkeletonProps extends Omit<HTMLAttributes<HTMLDivElement>, 'ref'> {
  ref?: Ref<HTMLDivElement>;
  variant?: SkeletonVariants['variant'];
  width?: string;
  height?: string;
  animated?: boolean;
}

export function Skeleton({ ref, variant = 'default', width, height, animated = true, className, style, ...rest }: SkeletonProps) {
  return (
    <div
      ref={ref}
      className={cn(skeletonVariants({ variant, animated }), className)}
      style={{ ...style, width, height }}
      aria-hidden="true"
      role="presentation"
      {...rest}
    />
  );
}

export default Skeleton;
