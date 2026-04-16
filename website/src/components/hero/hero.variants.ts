import { cva, type VariantProps } from 'class-variance-authority';

export const heroSectionVariants = cva('relative overflow-hidden bg-background', {
  variants: {
    size: {
      sm: 'pt-[var(--space-page-top-sm)] pb-[var(--space-section-sm)]',
      md: 'pt-[var(--space-page-top)] pb-[var(--space-section-md)]',
      lg: 'pt-[calc(var(--space-page-top)_+_var(--space-8))] pb-[var(--space-section-lg)]',
      xl: 'pt-[calc(var(--space-page-top)_+_var(--space-16))] pb-[var(--space-section-xl)]',
    },
  },
  defaultVariants: {
    size: 'lg',
  },
});

export type HeroSectionVariants = VariantProps<typeof heroSectionVariants>;
