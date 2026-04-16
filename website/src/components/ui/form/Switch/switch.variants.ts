import { cva, type VariantProps } from 'class-variance-authority';

export const switchTrackVariants = cva(
  [
    'relative inline-flex shrink-0 cursor-pointer rounded-full',
    'border-2 border-transparent',
    'transition-colors duration-200 ease-in-out',
    'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background',
    'disabled:cursor-not-allowed disabled:opacity-50',
  ],
  {
    variants: {
      size: {
        sm: 'h-5 w-9',
        md: 'h-6 w-11',
        lg: 'h-7 w-[3.25rem]',
      },
      checked: {
        true: 'bg-foreground',
        false: 'bg-border-strong',
      },
    },
    defaultVariants: {
      size: 'md',
      checked: false,
    },
  }
);

export const switchThumbVariants = cva(
  [
    'pointer-events-none inline-block rounded-full bg-background shadow-sm',
    'ring-0 transition-transform duration-200 ease-in-out',
  ],
  {
    variants: {
      size: {
        sm: 'h-4 w-4',
        md: 'h-5 w-5',
        lg: 'h-6 w-6',
      },
      checked: {
        true: '',
        false: 'translate-x-0',
      },
    },
    compoundVariants: [
      { checked: true, size: 'sm', class: 'translate-x-4' },
      { checked: true, size: 'md', class: 'translate-x-5' },
      { checked: true, size: 'lg', class: 'translate-x-6' },
    ],
    defaultVariants: {
      size: 'md',
      checked: false,
    },
  }
);

export type SwitchVariants = VariantProps<typeof switchTrackVariants>;
