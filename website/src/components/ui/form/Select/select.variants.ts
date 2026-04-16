import { cva, type VariantProps } from 'class-variance-authority';

export const selectVariants = cva(
  [
    'w-full rounded-lg border bg-background appearance-none cursor-pointer',
    'transition-colors duration-(--transition-fast)',
    'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
    'disabled:cursor-not-allowed disabled:opacity-50',
    'border-border',
  ],
  {
    variants: {
      size: {
        sm: 'h-8 px-3 text-sm',
        md: 'h-10 px-4 text-sm',
        lg: 'h-12 px-4 text-base',
      },
    },
    defaultVariants: {
      size: 'md',
    },
  }
);

export type SelectVariants = VariantProps<typeof selectVariants>;
