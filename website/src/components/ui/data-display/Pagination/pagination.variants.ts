import { cva, type VariantProps } from 'class-variance-authority';

export const paginationItemVariants = cva(
  [
    'inline-flex items-center justify-center rounded-md text-sm font-medium',
    'transition-colors duration-150',
    'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2',
  ],
  {
    variants: {
      variant: {
        default: 'hover:bg-secondary text-foreground-muted hover:text-foreground',
        active: 'bg-foreground text-background',
        disabled: 'text-foreground-subtle cursor-not-allowed opacity-50',
      },
      size: {
        sm: 'h-8 w-8 text-xs',
        md: 'h-9 w-9 text-sm',
        lg: 'h-10 w-10 text-base',
      },
    },
    defaultVariants: {
      variant: 'default',
      size: 'md',
    },
  }
);

export type PaginationVariants = VariantProps<typeof paginationItemVariants>;
