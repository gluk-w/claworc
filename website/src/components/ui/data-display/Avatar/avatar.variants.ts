import { cva, type VariantProps } from 'class-variance-authority';

export const avatarVariants = cva(
  [
    'relative inline-flex items-center justify-center',
    'rounded-full overflow-hidden',
    'bg-secondary text-secondary-foreground font-medium',
    'ring-2 ring-background',
  ],
  {
    variants: {
      size: {
        xs: 'w-6 h-6 text-[10px]',
        sm: 'w-8 h-8 text-xs',
        md: 'w-10 h-10 text-sm',
        lg: 'w-12 h-12 text-base',
        xl: 'w-16 h-16 text-lg',
      },
    },
    defaultVariants: {
      size: 'md',
    },
  }
);

export type AvatarVariants = VariantProps<typeof avatarVariants>;
