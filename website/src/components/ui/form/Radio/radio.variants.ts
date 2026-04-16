import { cva, type VariantProps } from 'class-variance-authority';

export const radioCircleVariants = cva(
  [
    'h-[18px] w-[18px] rounded-full',
    'border-2 border-border',
    'bg-background',
    'transition-all duration-150 ease-out',
    'peer-focus-visible:ring-2 peer-focus-visible:ring-ring peer-focus-visible:ring-offset-2 peer-focus-visible:ring-offset-background',
    'peer-checked:border-foreground',
  ],
  {
    variants: {
      variant: {
        default: '',
        card: 'h-5 w-5 peer-checked:bg-foreground',
      },
      error: {
        true: 'border-destructive peer-focus-visible:ring-destructive',
      },
    },
    defaultVariants: {
      variant: 'default',
    },
  }
);

export const radioCardVariants = cva(
  [
    'relative flex items-start gap-4 p-4 cursor-pointer',
    'rounded-xl border-2 border-border',
    'bg-background',
    'transition-all duration-150 ease-out',
    'hover:border-foreground-subtle hover:bg-secondary/30',
    'has-[:checked]:border-foreground has-[:checked]:bg-secondary/50',
    'has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-ring has-[:focus-visible]:ring-offset-2',
  ],
  {
    variants: {
      disabled: {
        true: 'cursor-not-allowed opacity-50',
      },
      error: {
        true: 'border-destructive',
      },
    },
  }
);

export type RadioVariants = VariantProps<typeof radioCircleVariants>;
