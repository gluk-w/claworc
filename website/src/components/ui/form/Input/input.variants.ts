import { cva, type VariantProps } from 'class-variance-authority';

export const inputVariants = cva(
  [
    'w-full rounded-lg border',
    'transition-colors duration-(--transition-fast)',
    'focus-visible:outline-none focus-visible:ring-2',
    'disabled:cursor-not-allowed disabled:opacity-50',
    'bg-background border-brand-500/30',
    'placeholder:text-muted-foreground',
    'focus-visible:ring-ring',
  ],
  {
    variants: {
      size: {
        sm: 'h-8 text-sm',
        md: 'h-10 text-sm',
        lg: 'h-12 text-base',
      },
    },
    defaultVariants: {
      size: 'md',
    },
  }
);

export const inputSizeConfig = {
  sm: {
    iconWrapper: 'w-8',
    leadingPadding: 'pl-8',
    trailingPadding: 'pr-8',
    baseLeftPadding: 'pl-3',
    baseRightPadding: 'pr-3',
  },
  md: {
    iconWrapper: 'w-10',
    leadingPadding: 'pl-10',
    trailingPadding: 'pr-10',
    baseLeftPadding: 'pl-4',
    baseRightPadding: 'pr-4',
  },
  lg: {
    iconWrapper: 'w-12',
    leadingPadding: 'pl-12',
    trailingPadding: 'pr-12',
    baseLeftPadding: 'pl-4',
    baseRightPadding: 'pr-4',
  },
} as const;

export type InputVariants = VariantProps<typeof inputVariants>;
