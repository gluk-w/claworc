import { cva, type VariantProps } from 'class-variance-authority';

export const checkboxBoxVariants = cva(
  [
    'h-[18px] w-[18px] shrink-0 rounded-[5px]',
    'border-2 border-border',
    'bg-background',
    'transition-all duration-150 ease-out',
    'peer-focus-visible:ring-2 peer-focus-visible:ring-ring peer-focus-visible:ring-offset-2 peer-focus-visible:ring-offset-background',
    'peer-checked:bg-foreground peer-checked:border-foreground',
  ],
  {
    variants: {
      error: {
        true: 'border-destructive peer-focus-visible:ring-destructive',
      },
    },
  }
);

export type CheckboxVariants = VariantProps<typeof checkboxBoxVariants>;
