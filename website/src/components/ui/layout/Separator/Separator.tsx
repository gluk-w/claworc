import { type HTMLAttributes, type Ref } from 'react';
import { cn } from '@/lib/cn';
import { separatorVariants, type SeparatorVariants } from './separator.variants';

interface SeparatorProps extends Omit<HTMLAttributes<HTMLDivElement>, 'ref'> {
  ref?: Ref<HTMLDivElement>;
  orientation?: NonNullable<SeparatorVariants['orientation']>;
  label?: string;
}

export function Separator({ ref, orientation = 'horizontal', label, className, ...rest }: SeparatorProps) {
  if (label) {
    return (
      <div
        ref={ref}
        className={cn(
          'flex items-center gap-3',
          orientation === 'vertical' && 'flex-col h-full',
          className
        )}
        role="separator"
        aria-orientation={orientation}
        {...rest}
      >
        <div className={cn(separatorVariants({ orientation }), 'flex-1')} />
        <span className="text-xs font-medium text-muted-foreground whitespace-nowrap">{label}</span>
        <div className={cn(separatorVariants({ orientation }), 'flex-1')} />
      </div>
    );
  }

  return (
    <div
      ref={ref}
      className={cn(separatorVariants({ orientation }), className)}
      role="separator"
      aria-orientation={orientation}
      {...rest}
    />
  );
}

export default Separator;
