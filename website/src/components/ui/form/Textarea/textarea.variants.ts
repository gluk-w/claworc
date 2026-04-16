import { cva, type VariantProps } from 'class-variance-authority';

export const textareaVariants = cva(
  [
    'w-full rounded-lg border bg-background resize-y min-h-[80px]',
    'transition-colors duration-(--transition-fast)',
    'placeholder:text-muted-foreground',
    'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
    'disabled:cursor-not-allowed disabled:opacity-50',
    'border-brand-500/30',
  ],
  {
    variants: {
      size: {
        sm: 'px-3 py-2 text-sm',
        md: 'px-4 py-3 text-sm',
        lg: 'px-4 py-3 text-base',
      },
    },
    defaultVariants: {
      size: 'md',
    },
  }
);

export type TextareaVariants = VariantProps<typeof textareaVariants>;
