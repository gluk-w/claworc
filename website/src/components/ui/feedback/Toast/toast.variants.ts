import { cva, type VariantProps } from 'class-variance-authority';

export const toastVariants = cva(
  [
    'pointer-events-auto relative flex items-start gap-3 overflow-hidden rounded-lg border p-4 shadow-lg',
    'transition-all duration-300 ease-out',
  ],
  {
    variants: {
      variant: {
        default: 'bg-card border-border text-foreground',
        success: 'bg-card border-[var(--success)]/30 text-foreground',
        error: 'bg-card border-[var(--error)]/30 text-foreground',
        warning: 'bg-card border-[var(--warning)]/30 text-foreground',
        info: 'bg-card border-[var(--info)]/30 text-foreground',
      },
    },
    defaultVariants: {
      variant: 'default',
    },
  }
);

export const toastIconColors = {
  default: 'text-foreground-muted',
  success: 'text-[var(--success)]',
  error: 'text-[var(--error)]',
  warning: 'text-[var(--warning)]',
  info: 'text-[var(--info)]',
} as const;

export type ToastVariants = VariantProps<typeof toastVariants>;
