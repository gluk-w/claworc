import { cva, type VariantProps } from 'class-variance-authority';

export const alertVariants = cva(
  'relative flex gap-4 p-4 rounded-lg border overflow-hidden',
  {
    variants: {
      variant: {
        info: 'bg-background border-border',
        success: 'bg-background border-[var(--success)]/30',
        warning: 'bg-background border-[var(--warning)]/30',
        error: 'bg-background border-[var(--error)]/30',
      },
    },
    defaultVariants: {
      variant: 'info',
    },
  }
);

export const alertIconColors = {
  info: 'text-foreground-muted',
  success: 'text-[var(--success)]',
  warning: 'text-[var(--warning)]',
  error: 'text-[var(--error)]',
} as const;

export const alertAccentColors = {
  info: 'bg-foreground-muted',
  success: 'bg-[var(--success)]',
  warning: 'bg-[var(--warning)]',
  error: 'bg-[var(--error)]',
} as const;

export type AlertVariants = VariantProps<typeof alertVariants>;
