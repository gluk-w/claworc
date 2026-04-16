import { cva, type VariantProps } from 'class-variance-authority';

export const footerVariants = cva('py-[var(--space-stack-lg)]', {
  variants: {
    background: {
      default: 'bg-background border-t border-border',
      secondary: 'bg-surface-secondary border-t border-border',
      invert: 'invert-section bg-background border-t border-border',
    },
  },
  defaultVariants: {
    background: 'default',
  },
});

export const footerColumnGridVariants = cva('grid grid-cols-1 gap-[var(--space-stack-lg)]', {
  variants: {
    columns: {
      2: 'md:grid-cols-2',
      3: 'md:grid-cols-3',
      4: 'md:grid-cols-4',
    },
  },
  defaultVariants: {
    columns: 3,
  },
});

export type FooterVariants = VariantProps<typeof footerVariants>;
export type FooterColumnGridVariants = VariantProps<typeof footerColumnGridVariants>;
