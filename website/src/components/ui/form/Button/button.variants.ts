import { cva, type VariantProps } from 'class-variance-authority';

export const buttonVariants = cva(
  [
    'inline-flex items-center justify-center gap-2',
    'font-medium rounded-md',
    'transition-all duration-150 ease-out',
    'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2',
    'disabled:pointer-events-none disabled:opacity-50',
    '[&_svg]:pointer-events-none [&_svg]:shrink-0',
  ],
  {
    variants: {
      variant: {
        primary:
          'btn-primary bg-foreground text-background hover:bg-foreground/90 active:scale-[0.98]',
        secondary:
          'bg-secondary text-secondary-foreground border border-border hover:bg-secondary-hover hover:border-border-strong active:scale-[0.98]',
        outline:
          'border border-foreground/25 bg-transparent text-foreground hover:bg-secondary hover:border-foreground/40 active:scale-[0.98]',
        ghost:
          'text-foreground-secondary hover:text-foreground hover:bg-secondary active:scale-[0.98]',
        link: 'text-foreground-secondary hover:text-foreground underline-offset-4 hover:underline',
        destructive:
          'bg-destructive text-destructive-foreground hover:bg-destructive/90 active:scale-[0.98]',
      },
      size: {
        sm: 'h-8 px-3 text-xs [&_svg]:h-4 [&_svg]:w-4',
        md: 'h-10 px-4 text-sm [&_svg]:h-5 [&_svg]:w-5',
        lg: 'h-12 px-5 text-base [&_svg]:h-5 [&_svg]:w-5',
      },
      fullWidth: {
        true: 'w-full',
      },
      icon: {
        true: 'rounded-md',
      },
    },
    compoundVariants: [
      { icon: true, size: 'sm', class: 'h-8 w-8 px-0' },
      { icon: true, size: 'md', class: 'h-10 w-10 px-0' },
      { icon: true, size: 'lg', class: 'h-12 w-12 px-0' },
    ],
    defaultVariants: {
      variant: 'primary',
      size: 'md',
    },
  }
);

export type ButtonVariants = VariantProps<typeof buttonVariants>;
