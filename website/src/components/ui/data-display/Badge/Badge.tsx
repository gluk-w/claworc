import { type HTMLAttributes, type Ref, type ReactNode } from 'react';
import { cn } from '@/lib/cn';
import { badgeVariants, type BadgeVariants } from './badge.variants';

interface BadgeProps extends Omit<HTMLAttributes<HTMLSpanElement>, 'ref'> {
  ref?: Ref<HTMLSpanElement>;
  variant?: BadgeVariants['variant'];
  size?: BadgeVariants['size'];
  pulse?: boolean;
  pill?: boolean;
  children?: ReactNode;
}

export function Badge({ ref, variant = 'default', size = 'md', pulse = false, pill = false, className, children, ...rest }: BadgeProps) {
  return (
    <span ref={ref} className={cn(badgeVariants({ variant, size, pill }), className)} {...rest}>
      {pulse && (
        <span className="flex h-2 w-2 shrink-0 animate-pulse rounded-full bg-brand-500" aria-hidden="true" />
      )}
      {children}
    </span>
  );
}

export default Badge;
