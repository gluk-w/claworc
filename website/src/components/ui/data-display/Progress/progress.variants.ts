import { cva, type VariantProps } from 'class-variance-authority';

export const progressTrackVariants = cva(
  'relative w-full overflow-hidden rounded-full bg-secondary',
  {
    variants: {
      size: {
        sm: 'h-1.5',
        md: 'h-2.5',
        lg: 'h-4',
      },
    },
    defaultVariants: {
      size: 'md',
    },
  }
);

export const progressBarVariants = cva(
  'h-full rounded-full transition-all duration-500 ease-out',
  {
    variants: {
      variant: {
        default: 'bg-foreground',
        brand: 'bg-brand-500',
        success: 'bg-[var(--success)]',
        warning: 'bg-[var(--warning)]',
        error: 'bg-[var(--error)]',
      },
      indeterminate: {
        true: 'animate-indeterminate w-1/3',
      },
    },
    defaultVariants: {
      variant: 'default',
    },
  }
);

export type ProgressVariants = VariantProps<typeof progressTrackVariants> &
  VariantProps<typeof progressBarVariants>;
