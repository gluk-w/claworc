import { cva, type VariantProps } from 'class-variance-authority';

export const badgeVariants = cva(
  [
    'inline-flex items-center font-medium border',
    'transition-colors',
    '[&>svg]:shrink-0 [&>svg]:h-3 [&>svg]:w-3',
  ],
  {
    variants: {
      variant: {
        default: 'bg-secondary text-secondary-foreground border-border',
        success:
          'bg-[var(--success-light)] text-[var(--success-foreground)] border-[var(--success)]/20',
        warning:
          'bg-[var(--warning-light)] text-[var(--warning-foreground)] border-[var(--warning)]/20',
        error:
          'bg-[var(--error-light)] text-[var(--error-foreground)] border-[var(--error)]/20',
        info: 'bg-[var(--info-light)] text-[var(--info-foreground)] border-[var(--info)]/20',
        brand:
          'bg-brand-500/10 text-brand-600 border-brand-500/20 dark:text-brand-400',
      },
      size: {
        sm: 'text-[10px] px-2 py-0.5 gap-1',
        md: 'text-sm sm:text-xs px-2.5 py-1 gap-1.5',
      },
      pill: {
        true: 'rounded-full shadow-sm',
        false: 'rounded-md',
      },
    },
    compoundVariants: [
      { pill: true, size: 'sm', class: 'px-2.5' },
      { pill: true, size: 'md', class: 'px-3.5 sm:px-3' },
    ],
    defaultVariants: {
      variant: 'default',
      size: 'md',
      pill: false,
    },
  }
);

export type BadgeVariants = VariantProps<typeof badgeVariants>;
